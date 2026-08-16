package proxyprovider

import (
	"context"
	stdjson "encoding/json"
	"errors"
	"hash/fnv"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
	F "github.com/sagernet/sing/common/format"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/service/filemanager"
	"github.com/sagernet/tailscale/atomicfile"
)

const (
	defaultProviderInterval = 24 * time.Hour
	initialRefreshDelay     = 100 * time.Millisecond
	minimumRetryDelay       = 15 * time.Second
	maximumRetryDelay       = 30 * time.Minute
)

type ManagerOptions struct {
	Context            context.Context
	Logger             log.ContextLogger
	LogFactory         log.Factory
	Router             adapter.Router
	OutboundRegistry   adapter.OutboundRegistry
	EndpointRegistry   adapter.EndpointRegistry
	OutboundManager    adapter.OutboundManager
	HTTPClientManager  adapter.HTTPClientManager
	Runtime            *RuntimeConfig
	AddOutbound        func(adapter.Outbound) error
	RemoveOutbound     func(string) error
	UpdateDependencies func(string, []string, []string)
}

type Manager struct {
	ctx                context.Context
	cancel             context.CancelFunc
	logger             log.ContextLogger
	logFactory         log.Factory
	router             adapter.Router
	outboundRegistry   adapter.OutboundRegistry
	endpointRegistry   adapter.EndpointRegistry
	outboundManager    adapter.OutboundManager
	httpClientManager  adapter.HTTPClientManager
	runtime            *RuntimeConfig
	addOutbound        func(adapter.Outbound) error
	removeOutbound     func(string) error
	updateDependencies func(string, []string, []string)

	updateAccess    sync.Mutex
	transportAccess sync.Mutex
	transports      map[string]adapter.HTTPTransport
	contents        map[string]providerContent
	wrappers        map[string]*dynamicOutbound
	waitGroup       sync.WaitGroup
	closeOnce       sync.Once
}

type providerFetchResult struct {
	content      []byte
	notModified  bool
	etag         string
	lastModified string
}

type providerTarget struct {
	outbound adapter.Outbound
	server   M.Socksaddr
}

func NewManager(options ManagerOptions) (*Manager, error) {
	if options.Runtime == nil {
		return nil, nil
	}
	ctx, cancel := context.WithCancel(options.Context)
	manager := &Manager{
		ctx:                ctx,
		cancel:             cancel,
		logger:             options.Logger,
		logFactory:         options.LogFactory,
		router:             options.Router,
		outboundRegistry:   options.OutboundRegistry,
		endpointRegistry:   options.EndpointRegistry,
		outboundManager:    options.OutboundManager,
		httpClientManager:  options.HTTPClientManager,
		runtime:            options.Runtime,
		addOutbound:        options.AddOutbound,
		removeOutbound:     options.RemoveOutbound,
		updateDependencies: options.UpdateDependencies,
		transports:         make(map[string]adapter.HTTPTransport),
		contents:           make(map[string]providerContent),
		wrappers:           make(map[string]*dynamicOutbound),
	}
	for name, provider := range options.Runtime.resolved {
		if len(provider.content.content) > 0 {
			manager.contents[name] = provider.content
		}
	}
	if err := manager.validateDownloadDependencies(); err != nil {
		cancel()
		return nil, err
	}
	targets, err := manager.createTargets(options.Runtime.resolved)
	if err != nil {
		cancel()
		return nil, err
	}
	var added []*dynamicOutbound
	for _, tag := range sortedTargetTags(targets) {
		target := targets[tag]
		wrapper := newDynamicOutbound(tag, target.outbound, target.server)
		if err = manager.addOutbound(wrapper); err != nil {
			for _, previous := range added {
				_ = manager.removeOutbound(previous.Tag())
				_ = previous.Close()
			}
			for remainingTag, target := range targets {
				if _, loaded := manager.wrappers[remainingTag]; !loaded {
					_ = closeProviderOutbound(target.outbound)
				}
			}
			cancel()
			return nil, E.Cause(err, "register proxy-provider outbound ", tag)
		}
		manager.wrappers[tag] = wrapper
		added = append(added, wrapper)
	}
	return manager, nil
}

func (m *Manager) Name() string {
	return "proxy-provider"
}

func (m *Manager) Start(stage adapter.StartStage) error {
	if stage != adapter.StartStateStarted {
		return nil
	}
	for _, name := range sortedProviderNames(m.runtime.providers) {
		provider := m.runtime.providers[name]
		if !strings.EqualFold(provider.Type, "http") {
			continue
		}
		m.waitGroup.Add(1)
		go m.runProvider(name, provider)
	}
	return nil
}

func (m *Manager) Close() error {
	m.closeOnce.Do(func() {
		m.cancel()
		m.waitGroup.Wait()
		m.transportAccess.Lock()
		for _, transport := range m.transports {
			transport.CloseIdleConnections()
		}
		m.transports = nil
		m.transportAccess.Unlock()
	})
	return nil
}

func (m *Manager) runProvider(name string, provider option.ProxyProvider) {
	defer m.waitGroup.Done()
	interval := providerRefreshInterval(provider)
	delay := providerInitialRefreshDelay(name)
	m.updateAccess.Lock()
	if content, loaded := m.contents[name]; loaded && !content.modTime.IsZero() {
		if remaining := interval - time.Since(content.modTime); remaining > initialRefreshDelay {
			delay = remaining
		}
	}
	m.updateAccess.Unlock()
	retryDelay := minimumRetryDelay
	timer := time.NewTimer(delay)
	defer timer.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-timer.C:
		}
		err := m.refreshProvider(name, provider)
		if err != nil {
			m.logger.Warn("refresh proxy-provider ", name, ": ", sanitizeProviderError(err))
			timer.Reset(retryDelay)
			retryDelay *= 2
			if retryDelay > maximumRetryDelay {
				retryDelay = maximumRetryDelay
			}
			continue
		}
		retryDelay = minimumRetryDelay
		timer.Reset(interval)
	}
}

func providerInitialRefreshDelay(name string) time.Duration {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(name))
	return initialRefreshDelay + time.Duration(hash.Sum32()%900)*time.Millisecond
}

func providerRefreshInterval(provider option.ProxyProvider) time.Duration {
	if provider.Interval <= 0 {
		return defaultProviderInterval
	}
	return time.Duration(provider.Interval) * time.Second
}

func (m *Manager) refreshProvider(name string, provider option.ProxyProvider) error {
	m.updateAccess.Lock()
	previousContent := m.contents[name]
	m.updateAccess.Unlock()
	fetched, err := m.fetchProvider(name, provider, previousContent)
	if err != nil {
		return err
	}
	m.updateAccess.Lock()
	defer m.updateAccess.Unlock()
	if fetched.notModified {
		current, loaded := m.contents[name]
		if !loaded || len(current.content) == 0 {
			return E.New("received not modified response without cached content")
		}
		current.modTime = time.Now()
		if fetched.etag != "" {
			current.etag = fetched.etag
		}
		if fetched.lastModified != "" {
			current.modified = fetched.lastModified
		}
		m.contents[name] = current
		if err = saveProviderMetadata(current.cachePath, current); err != nil {
			m.logger.Warn("save proxy-provider ", name, " metadata: ", err)
		}
		m.logger.Debug("proxy-provider ", name, " not modified")
		return nil
	}

	candidateContents := make(map[string]providerContent, len(m.contents)+1)
	for providerName, existing := range m.contents {
		candidateContents[providerName] = existing
	}
	cachePath := providerCachePath(m.ctx, name, provider)
	candidateContents[name] = providerContent{
		content:     fetched.content,
		cachePath:   cachePath,
		source:      providerContentSourceCache,
		modTime:     time.Now(),
		etag:        fetched.etag,
		modified:    fetched.lastModified,
		fingerprint: providerSourceFingerprint(provider),
	}
	resolved, err := m.resolveContents(candidateContents)
	if err != nil {
		return err
	}
	groupMembers, err := m.resolveGroupMembers(resolved)
	if err != nil {
		return err
	}
	targets, err := m.createTargets(resolved)
	if err != nil {
		return err
	}
	if err = m.publish(targets, groupMembers); err != nil {
		return err
	}
	if err = saveProviderCacheAtomic(cachePath, fetched.content); err != nil {
		m.logger.Warn("save proxy-provider ", name, " cache: ", err)
	} else if metadataErr := saveProviderMetadata(cachePath, candidateContents[name]); metadataErr != nil {
		m.logger.Warn("save proxy-provider ", name, " metadata: ", metadataErr)
	}
	m.contents = candidateContents
	m.runtime.resolved = resolved
	m.logger.Info("updated proxy-provider ", name)
	return nil
}

func (m *Manager) fetchProvider(name string, provider option.ProxyProvider, previous providerContent) (providerFetchResult, error) {
	transport, err := m.providerTransport(name, provider)
	if err != nil {
		return providerFetchResult{}, err
	}
	return fetchProvider(m.ctx, provider, previous, transport)
}

func fetchProvider(ctx context.Context, provider option.ProxyProvider, previous providerContent, transport http.RoundTripper) (providerFetchResult, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, provider.URL, nil)
	if err != nil {
		return providerFetchResult{}, sanitizeProviderError(err)
	}
	for headerName, values := range provider.Header {
		for _, value := range values {
			request.Header.Add(headerName, value)
		}
	}
	if previous.etag != "" {
		request.Header.Set("If-None-Match", previous.etag)
	}
	if previous.modified != "" {
		request.Header.Set("If-Modified-Since", previous.modified)
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}
	response, err := client.Do(request)
	if err != nil {
		return providerFetchResult{}, sanitizeProviderError(err)
	}
	defer response.Body.Close()
	result := providerFetchResult{
		etag:         response.Header.Get("ETag"),
		lastModified: response.Header.Get("Last-Modified"),
	}
	if response.StatusCode == http.StatusNotModified {
		result.notModified = true
		return result, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return providerFetchResult{}, E.New("unexpected status: ", response.Status)
	}
	result.content, err = readAllLimited(response.Body, 16<<20)
	if err != nil {
		return providerFetchResult{}, err
	}
	return result, nil
}

func (m *Manager) providerTransport(name string, provider option.ProxyProvider) (adapter.HTTPTransport, error) {
	m.transportAccess.Lock()
	defer m.transportAccess.Unlock()
	if transport := m.transports[name]; transport != nil {
		return transport, nil
	}
	var options option.HTTPClientOptions
	providerProxy := strings.TrimSpace(provider.Proxy)
	if providerProxy != "" && !isDirectProviderProxy(providerProxy) {
		options.Detour = providerProxy
	}
	transport, err := m.httpClientManager.ResolveTransport(m.ctx, m.logger, options)
	if err != nil {
		return nil, err
	}
	m.transports[name] = transport
	return transport, nil
}

func (m *Manager) resolveContents(contents map[string]providerContent) (map[string]resolvedProvider, error) {
	resolved := make(map[string]resolvedProvider, len(m.runtime.providers))
	usedTags := cloneTagSet(m.runtime.staticTags)
	for _, name := range sortedProviderNames(m.runtime.providers) {
		provider := m.runtime.providers[name]
		content, loaded := contents[name]
		if !loaded || len(content.content) == 0 {
			resolved[name] = resolvedProvider{}
			continue
		}
		current, err := resolveProviderContent(m.ctx, m.logger, name, provider, content, usedTags, m.runtime.domainResolver)
		if err != nil {
			return nil, E.Cause(err, "proxy-provider ", name)
		}
		resolved[name] = current
	}
	return resolved, nil
}

func (m *Manager) resolveGroupMembers(resolved map[string]resolvedProvider) (map[string][]string, error) {
	available := cloneTagSet(m.runtime.staticTags)
	if m.runtime.fallbackTag != "" {
		available[m.runtime.fallbackTag] = true
	}
	for _, provider := range resolved {
		for _, proxy := range provider.proxies {
			available[proxy.tag] = true
		}
	}
	groups := make(map[string][]string, len(m.runtime.groups))
	for _, definition := range m.runtime.groups {
		members, err := expandUse(
			definition.tag,
			definition.outbounds,
			definition.use,
			resolved,
			definition.filter,
			definition.excludeFilter,
			definition.excludeType,
		)
		if err != nil {
			return nil, err
		}
		members = slices.DeleteFunc(members, func(tag string) bool {
			return !available[tag]
		})
		if len(members) == 0 && m.runtime.fallbackTag != "" {
			members = []string{m.runtime.fallbackTag}
		}
		groups[definition.tag] = members
	}
	return groups, nil
}

func (m *Manager) createTargets(resolved map[string]resolvedProvider) (map[string]providerTarget, error) {
	targets := make(map[string]providerTarget)
	for _, name := range sortedProviderNames(m.runtime.providers) {
		provider := resolved[name]
		for _, outboundOptions := range provider.outbounds {
			target, err := m.outboundRegistry.CreateOutbound(
				m.ctx,
				m.router,
				m.logFactory.NewLogger(F.ToString("proxy-provider/outbound/", outboundOptions.Type, "[", outboundOptions.Tag, "]")),
				outboundOptions.Tag,
				outboundOptions.Type,
				outboundOptions.Options,
			)
			if err != nil {
				closeProviderTargets(targets)
				return nil, E.Cause(err, "create proxy-provider outbound ", outboundOptions.Tag)
			}
			targets[outboundOptions.Tag] = providerTarget{
				outbound: target,
				server:   serverAddressFromOptions(outboundOptions.Options),
			}
		}
		for _, endpointOptions := range provider.endpoints {
			target, err := m.endpointRegistry.Create(
				m.ctx,
				m.router,
				m.logFactory.NewLogger(F.ToString("proxy-provider/endpoint/", endpointOptions.Type, "[", endpointOptions.Tag, "]")),
				endpointOptions.Tag,
				endpointOptions.Type,
				endpointOptions.Options,
			)
			if err != nil {
				closeProviderTargets(targets)
				return nil, E.Cause(err, "create proxy-provider endpoint ", endpointOptions.Tag)
			}
			targets[endpointOptions.Tag] = providerTarget{
				outbound: target,
				server:   serverAddressFromOptions(endpointOptions.Options),
			}
		}
	}
	return targets, nil
}

func (m *Manager) publish(targets map[string]providerTarget, groupMembers map[string][]string) error {
	type groupUpdate struct {
		tag      string
		group    adapter.OutboundGroupUpdater
		previous []string
		current  []string
	}
	var groupUpdates []groupUpdate
	for _, definition := range m.runtime.groups {
		outbound, loaded := m.outboundManager.Outbound(definition.tag)
		if !loaded {
			closeProviderTargets(targets)
			return E.New("outbound group not found during provider update: ", definition.tag)
		}
		group, loaded := outbound.(adapter.OutboundGroupUpdater)
		if !loaded {
			closeProviderTargets(targets)
			return E.New("outbound group does not support provider updates: ", definition.tag)
		}
		current := groupMembers[definition.tag]
		for _, tag := range current {
			if _, created := targets[tag]; created {
				continue
			}
			if _, exists := m.outboundManager.Outbound(tag); !exists {
				closeProviderTargets(targets)
				return E.New("provider group member not found: ", tag)
			}
		}
		groupUpdates = append(groupUpdates, groupUpdate{
			tag:      definition.tag,
			group:    group,
			previous: group.All(),
			current:  current,
		})
	}

	existingTags := make(map[string]bool, len(m.wrappers))
	for tag := range m.wrappers {
		existingTags[tag] = true
	}

	var added []string
	cleanup := func() {
		for _, addedTag := range added {
			_ = m.removeOutbound(addedTag)
			delete(m.wrappers, addedTag)
			delete(targets, addedTag)
		}
		closeProviderTargets(targets)
	}
	for _, tag := range sortedTargetTags(targets) {
		if m.wrappers[tag] != nil {
			continue
		}
		target := targets[tag]
		wrapper := newDynamicOutbound(tag, target.outbound, target.server)
		if err := m.addOutbound(wrapper); err != nil {
			cleanup()
			return E.Cause(err, "publish proxy-provider outbound ", tag)
		}
		m.wrappers[tag] = wrapper
		added = append(added, tag)
	}

	for _, tag := range sortedTargetTags(targets) {
		if !existingTags[tag] {
			continue
		}
		if err := startProviderOutbound(targets[tag].outbound); err != nil {
			cleanup()
			return E.Cause(err, "start replacement proxy-provider outbound ", tag)
		}
	}

	var appliedGroups []groupUpdate
	for _, update := range groupUpdates {
		if err := update.group.UpdateOutbounds(update.current); err != nil {
			for index := len(appliedGroups) - 1; index >= 0; index-- {
				applied := appliedGroups[index]
				_ = applied.group.UpdateOutbounds(applied.previous)
				if m.updateDependencies != nil {
					m.updateDependencies(applied.tag, applied.current, applied.previous)
				}
			}
			cleanup()
			return E.Cause(err, "update outbound group ", update.tag)
		}
		if m.updateDependencies != nil {
			m.updateDependencies(update.tag, update.previous, update.current)
		}
		appliedGroups = append(appliedGroups, update)
	}

	replaced := make(map[string]adapter.Outbound)
	for _, tag := range sortedTargetTags(targets) {
		if !existingTags[tag] {
			continue
		}
		wrapper := m.wrappers[tag]
		target := targets[tag]
		previous := wrapper.replace(target.outbound, target.server)
		replaced[tag] = previous
		if m.updateDependencies != nil {
			m.updateDependencies(tag, previous.Dependencies(), target.outbound.Dependencies())
		}
	}

	for tag := range existingTags {
		if _, exists := targets[tag]; exists {
			continue
		}
		if err := m.removeOutbound(tag); err != nil {
			m.logger.Warn("keep obsolete proxy-provider outbound ", tag, ": ", err)
			continue
		}
		delete(m.wrappers, tag)
	}
	for _, previous := range replaced {
		_ = closeProviderOutbound(previous)
	}
	return nil
}

func (m *Manager) validateDownloadDependencies() error {
	for providerName, provider := range m.runtime.providers {
		proxy := strings.TrimSpace(provider.Proxy)
		if proxy == "" || isDirectProviderProxy(proxy) {
			continue
		}
		if resolved := m.runtime.resolved[providerName]; providerContainsTag(resolved, proxy) {
			return E.New("proxy-provider ", providerName, " download proxy depends on itself: ", proxy)
		}
		proxyExists := m.runtime.staticTags[proxy]
		if !proxyExists {
			for _, resolved := range m.runtime.resolved {
				if providerContainsTag(resolved, proxy) {
					proxyExists = true
					break
				}
			}
		}
		if !proxyExists {
			return E.New("proxy-provider ", providerName, " download proxy not found: ", proxy)
		}
		for _, group := range m.runtime.groups {
			if group.tag != proxy {
				continue
			}
			if len(group.use) == 0 && (group.filter != "" || group.excludeFilter != "" || group.excludeType != "") ||
				slices.Contains(group.use, "*") ||
				slices.Contains(group.use, providerName) {
				return E.New("proxy-provider ", providerName, " download proxy group depends on the provider itself: ", proxy)
			}
		}
	}
	return nil
}

func providerContainsTag(provider resolvedProvider, tag string) bool {
	for _, proxy := range provider.proxies {
		if proxy.tag == tag {
			return true
		}
	}
	return false
}

func providerCachePath(ctx context.Context, name string, provider option.ProxyProvider) string {
	cachePath := provider.Path
	if cachePath == "" {
		cachePath = filepath.Join("proxy_providers", safeFileName(name)+".yaml")
	}
	return filemanager.BasePath(ctx, filepath.Clean(cachePath))
}

func saveProviderCacheAtomic(cachePath string, content []byte) error {
	if cachePath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		return err
	}
	return atomicfile.WriteFile(cachePath, content, 0o600)
}

func providerMetadataPath(cachePath string) string {
	return cachePath + ".meta.json"
}

func saveProviderMetadata(cachePath string, content providerContent) error {
	if cachePath == "" {
		return nil
	}
	metadata, err := stdjson.Marshal(providerCacheMetadata{
		ETag:              content.etag,
		LastModified:      content.modified,
		UpdatedAt:         content.modTime,
		ContentSHA256:     contentSHA256(content.content),
		SourceFingerprint: content.fingerprint,
	})
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(providerMetadataPath(cachePath), metadata, 0o600)
}

func sanitizeProviderError(err error) error {
	var urlError *url.Error
	if errors.As(err, &urlError) {
		return urlError.Err
	}
	return err
}

func sortedProviderNames(providers map[string]option.ProxyProvider) []string {
	names := make([]string, 0, len(providers))
	for name := range providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedTargetTags(targets map[string]providerTarget) []string {
	tags := make([]string, 0, len(targets))
	for tag := range targets {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	return tags
}

func startProviderOutbound(outbound adapter.Outbound) error {
	for _, stage := range adapter.ListStartStages {
		if err := adapter.LegacyStart(outbound, stage); err != nil {
			return err
		}
	}
	return nil
}

func closeProviderTargets(targets map[string]providerTarget) {
	for _, target := range targets {
		_ = closeProviderOutbound(target.outbound)
	}
}

func serverAddressFromOptions(options any) M.Socksaddr {
	if serverOptions, loaded := options.(option.ServerOptionsWrapper); loaded {
		return serverOptions.TakeServerOptions().Build()
	}
	return M.Socksaddr{}
}

package proxyprovider

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/service/filemanager"

	"gopkg.in/yaml.v3"
)

type subscriptionFile struct {
	Proxies []map[string]any `yaml:"proxies"`
}

type resolvedProvider struct {
	proxies   []resolvedProxy
	outbounds []option.Outbound
	endpoints []option.Endpoint
}

type resolvedProxy struct {
	tag          string
	name         string
	providerName string
	proxyType    string
}

type providerContentUnavailable struct {
	err error
}

type providerContentSource uint8

const (
	providerContentSourceFile providerContentSource = iota
	providerContentSourceCache
	providerContentSourceFetched
)

type providerContent struct {
	content   []byte
	cachePath string
	source    providerContentSource
}

func (e providerContentUnavailable) Error() string {
	return e.err.Error()
}

func (e providerContentUnavailable) Unwrap() error {
	return e.err
}

func Expand(ctx context.Context, logger log.ContextLogger, options *option.Options) error {
	if len(options.ProxyProviders) == 0 {
		return nil
	}
	usedTags := existingOutboundTags(options)
	domainResolver := providerDomainResolver(options)
	providers := make(map[string]resolvedProvider, len(options.ProxyProviders))
	providerNames := make([]string, 0, len(options.ProxyProviders))
	for name := range options.ProxyProviders {
		providerNames = append(providerNames, name)
	}
	sort.Strings(providerNames)
	hasUnavailableProvider := false
	for _, name := range providerNames {
		providerOptions := options.ProxyProviders[name]
		if options.ProxyProviderDefaults != nil {
			providerOptions = mergeProxyProviderDefaults(*options.ProxyProviderDefaults, providerOptions)
		}
		provider, err := resolveProvider(ctx, logger, name, providerOptions, usedTags, domainResolver)
		if err != nil {
			var unavailable providerContentUnavailable
			if errors.As(err, &unavailable) {
				logger.Warn("proxy-provider ", name, " unavailable without usable cached content, skipping: ", unavailable.err)
				providers[name] = resolvedProvider{}
				hasUnavailableProvider = true
				continue
			}
			return E.Cause(err, "proxy-provider ", name)
		}
		providers[name] = provider
		options.Endpoints = append(options.Endpoints, provider.endpoints...)
		options.Outbounds = append(options.Outbounds, provider.outbounds...)
	}
	for i := range options.Outbounds {
		if err := expandOutboundUse(&options.Outbounds[i], providers); err != nil {
			return err
		}
	}
	pruneMissingGroupDependencies(logger, options, hasUnavailableProvider)
	return nil
}

func providerDomainResolver(options *option.Options) string {
	if options.Route != nil && options.Route.DefaultDomainResolver != nil && options.Route.DefaultDomainResolver.Server != "" {
		return options.Route.DefaultDomainResolver.Server
	}
	if options.DNS != nil {
		for _, server := range options.DNS.Servers {
			if server.Tag != "" {
				return server.Tag
			}
		}
	}
	return ""
}

func mergeProxyProviderDefaults(defaults option.ProxyProvider, provider option.ProxyProvider) option.ProxyProvider {
	skipDefaultInsecure := strings.EqualFold(provider.Type, "file")
	if provider.Type == "" {
		provider.Type = defaults.Type
	}
	if provider.URL == "" {
		provider.URL = defaults.URL
	}
	if provider.Path == "" {
		provider.Path = defaults.Path
	}
	if provider.Proxy == "" {
		provider.Proxy = defaults.Proxy
	}
	if provider.Interval == 0 {
		provider.Interval = defaults.Interval
	}
	if provider.Filter == "" {
		provider.Filter = defaults.Filter
	}
	if provider.ExcludeFilter == "" {
		provider.ExcludeFilter = defaults.ExcludeFilter
	}
	if provider.ExcludeType == "" {
		provider.ExcludeType = defaults.ExcludeType
	}
	if len(provider.Header) == 0 {
		provider.Header = defaults.Header
	}
	// Manual file providers should keep each node's TLS verification setting.
	if skipDefaultInsecure {
		defaults.Override.Insecure = nil
	}
	provider.Override = mergeProxyProviderOverride(defaults.Override, provider.Override)
	return provider
}

func mergeProxyProviderOverride(defaults option.ProxyProviderOverride, override option.ProxyProviderOverride) option.ProxyProviderOverride {
	if override.UDP == nil {
		override.UDP = defaults.UDP
	}
	if override.IPVersion == "" {
		override.IPVersion = defaults.IPVersion
	}
	if override.Insecure == nil {
		override.Insecure = defaults.Insecure
	}
	if override.AdditionalPrefix == "" {
		override.AdditionalPrefix = defaults.AdditionalPrefix
	}
	return override
}

func existingOutboundTags(options *option.Options) map[string]bool {
	used := make(map[string]bool)
	for i, outbound := range options.Outbounds {
		tag := outbound.Tag
		if tag == "" {
			tag = stringIndex(i)
		}
		used[tag] = true
	}
	for i, endpoint := range options.Endpoints {
		tag := endpoint.Tag
		if tag == "" {
			tag = stringIndex(i)
		}
		used[tag] = true
	}
	return used
}

func expandOutboundUse(outbound *option.Outbound, providers map[string]resolvedProvider) error {
	switch outbound.Type {
	case "selector":
		options, ok := outbound.Options.(*option.SelectorOutboundOptions)
		if !ok {
			return nil
		}
		expanded, err := expandUse(outbound.Tag, options.Outbounds, options.Use, providers, options.Filter, options.ExcludeFilter, options.ExcludeType)
		if err != nil {
			return err
		}
		options.Outbounds = expanded
	case "urltest":
		options, ok := outbound.Options.(*option.URLTestOutboundOptions)
		if !ok {
			return nil
		}
		expanded, err := expandUse(outbound.Tag, options.Outbounds, options.Use, providers, options.Filter, options.ExcludeFilter, options.ExcludeType)
		if err != nil {
			return err
		}
		options.Outbounds = expanded
	}
	return nil
}

func expandUse(groupTag string, outbounds []string, uses []string, providers map[string]resolvedProvider, filterPattern string, excludeFilterPattern string, excludeTypePattern string) ([]string, error) {
	if len(uses) == 0 {
		if filterPattern == "" && excludeFilterPattern == "" && excludeTypePattern == "" {
			return outbounds, nil
		}
		uses = []string{"*"}
	}
	filter, err := compileProviderFilter(filterPattern)
	if err != nil {
		return nil, E.Cause(err, "outbound group ", groupTag, " filter")
	}
	excludeFilter, err := compileProviderFilter(excludeFilterPattern)
	if err != nil {
		return nil, E.Cause(err, "outbound group ", groupTag, " exclude-filter")
	}
	excludeType, err := compileProviderFilter(excludeTypePattern)
	if err != nil {
		return nil, E.Cause(err, "outbound group ", groupTag, " exclude-type")
	}
	seen := make(map[string]bool, len(outbounds))
	expanded := append([]string{}, outbounds...)
	for _, tag := range outbounds {
		seen[tag] = true
	}
	providerNames, err := expandUseProviderNames(groupTag, uses, providers)
	if err != nil {
		return nil, err
	}
	for _, providerName := range providerNames {
		provider := providers[providerName]
		for _, proxy := range provider.proxies {
			if !matchProxyNameFilter(filter, proxy, true) || matchProxyNameExcludeFilter(excludeFilter, proxy) || matchProxyTypeName(excludeType, proxy.proxyType) {
				continue
			}
			if seen[proxy.tag] {
				continue
			}
			expanded = append(expanded, proxy.tag)
			seen[proxy.tag] = true
		}
	}
	return expanded, nil
}

func expandUseProviderNames(groupTag string, uses []string, providers map[string]resolvedProvider) ([]string, error) {
	seen := make(map[string]bool, len(providers))
	var providerNames []string
	appendProvider := func(providerName string) error {
		_, loaded := providers[providerName]
		if !loaded {
			return E.New("outbound group ", groupTag, " references unknown proxy-provider: ", providerName)
		}
		if !seen[providerName] {
			providerNames = append(providerNames, providerName)
			seen[providerName] = true
		}
		return nil
	}
	for _, providerName := range uses {
		if providerName == "*" {
			allProviders := make([]string, 0, len(providers))
			for name := range providers {
				allProviders = append(allProviders, name)
			}
			sort.Strings(allProviders)
			for _, name := range allProviders {
				if err := appendProvider(name); err != nil {
					return nil, err
				}
			}
			continue
		}
		if err := appendProvider(providerName); err != nil {
			return nil, err
		}
	}
	return providerNames, nil
}

func resolveProvider(ctx context.Context, logger log.ContextLogger, name string, provider option.ProxyProvider, usedTags map[string]bool, domainResolver string) (resolvedProvider, error) {
	if provider.Type == "" {
		if provider.URL != "" {
			provider.Type = "http"
		} else {
			provider.Type = "file"
		}
	}
	loadedContent, err := loadProviderContent(ctx, logger, name, provider)
	if err != nil {
		return resolvedProvider{}, err
	}
	subscription, err := resolveProviderSubscription(ctx, logger, name, provider, loadedContent)
	if err != nil {
		return resolvedProvider{}, err
	}
	filter, err := compileProviderFilter(provider.Filter)
	if err != nil {
		return resolvedProvider{}, E.Cause(err, "filter")
	}
	excludeFilter, err := compileProviderFilter(provider.ExcludeFilter)
	if err != nil {
		return resolvedProvider{}, E.Cause(err, "exclude-filter")
	}
	excludeType, err := compileProviderFilter(provider.ExcludeType)
	if err != nil {
		return resolvedProvider{}, E.Cause(err, "exclude-type")
	}
	var resolved resolvedProvider
	for index, proxy := range subscription.Proxies {
		rawName := stringValue(proxy, "name")
		if rawName == "" {
			return resolvedProvider{}, E.New("proxy ", index, " missing name")
		}
		displayName := provider.Override.AdditionalPrefix + rawName
		if !matchFilterValues(filter, true, rawName, displayName) || matchFilterValues(excludeFilter, false, rawName, displayName, name) || matchProxyType(excludeType, proxy) {
			continue
		}
		converted, err := convertProxy(provider, proxy, usedTags, domainResolver)
		if err != nil {
			var unsupported unsupportedProxyTypeError
			if errors.As(err, &unsupported) {
				logger.Warn("proxy-provider ", name, " proxy ", index, " [", rawName, "] skipped: ", unsupported)
				continue
			}
			return resolvedProvider{}, E.Cause(err, "proxy ", index, " [", rawName, "]")
		}
		resolved.proxies = append(resolved.proxies, resolvedProxy{
			tag:          converted.tag,
			name:         rawName,
			providerName: name,
			proxyType:    converted.proxyType,
		})
		if converted.endpoint != nil {
			// WireGuard is an endpoint in current sing-box, but groups still select it by tag through OutboundManager.
			resolved.endpoints = append(resolved.endpoints, *converted.endpoint)
		} else if converted.outbound != nil {
			resolved.outbounds = append(resolved.outbounds, *converted.outbound)
		}
	}
	if len(resolved.outbounds) == 0 && len(resolved.endpoints) == 0 {
		return resolvedProvider{}, E.New("subscription does not contain usable proxies")
	}
	return resolved, nil
}

func resolveProviderSubscription(ctx context.Context, logger log.ContextLogger, name string, provider option.ProxyProvider, loaded providerContent) (subscriptionFile, error) {
	subscription, err := parseNonEmptySubscription(loaded.content)
	if err == nil {
		if loaded.source == providerContentSourceFetched {
			saveProviderCache(logger, name, loaded.cachePath, loaded.content)
		}
		return subscription, nil
	}
	if !strings.EqualFold(provider.Type, "http") {
		return subscriptionFile{}, err
	}
	switch loaded.source {
	case providerContentSourceFetched:
		cached, readErr := os.ReadFile(loaded.cachePath)
		if readErr != nil {
			return subscriptionFile{}, providerContentUnavailable{err: err}
		}
		cachedSubscription, cachedErr := parseNonEmptySubscription(cached)
		if cachedErr != nil {
			logger.Warn("fetch proxy-provider ", name, " returned unusable content and cached content is unusable: ", cachedErr)
			return subscriptionFile{}, providerContentUnavailable{err: err}
		}
		logger.Warn("fetch proxy-provider ", name, " returned unusable content, using cached content: ", err)
		return cachedSubscription, nil
	case providerContentSourceCache:
		fetched, fetchErr := fetchProviderContent(ctx, provider)
		if fetchErr != nil {
			logger.Warn("cached proxy-provider ", name, " content is unusable and refetch failed: ", fetchErr)
			return subscriptionFile{}, providerContentUnavailable{err: err}
		}
		fetchedSubscription, fetchedErr := parseNonEmptySubscription(fetched)
		if fetchedErr != nil {
			logger.Warn("cached proxy-provider ", name, " content is unusable and refetched content is unusable: ", fetchedErr)
			return subscriptionFile{}, providerContentUnavailable{err: err}
		}
		logger.Warn("cached proxy-provider ", name, " content is unusable, using refetched content: ", err)
		saveProviderCache(logger, name, loaded.cachePath, fetched)
		return fetchedSubscription, nil
	default:
		return subscriptionFile{}, providerContentUnavailable{err: err}
	}
}

func parseNonEmptySubscription(content []byte) (subscriptionFile, error) {
	subscription, err := parseSubscription(content)
	if err != nil {
		return subscriptionFile{}, err
	}
	if len(subscription.Proxies) == 0 {
		return subscriptionFile{}, E.New("subscription does not contain proxies")
	}
	return subscription, nil
}

func parseSubscription(content []byte) (subscriptionFile, error) {
	var subscription subscriptionFile
	yamlErr := yaml.Unmarshal(content, &subscription)
	if yamlErr == nil && len(subscription.Proxies) > 0 {
		return subscription, nil
	}
	if uriSubscription := parseURIListSubscription(content); len(uriSubscription.Proxies) > 0 {
		return uriSubscription, nil
	}
	if yamlErr != nil {
		return subscriptionFile{}, E.Cause(yamlErr, "decode subscription")
	}
	return subscription, nil
}

func parseURIListSubscription(content []byte) subscriptionFile {
	textCandidates := []string{string(content)}
	if decoded, ok := decodeBase64Subscription(content); ok {
		textCandidates = append([]string{decoded}, textCandidates...)
	}
	var subscription subscriptionFile
	for _, text := range textCandidates {
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "://") {
				continue
			}
			uri, err := url.Parse(line)
			if err != nil {
				continue
			}
			switch strings.ToLower(uri.Scheme) {
			case "tuic":
				if proxy := proxyFromTUICURI(uri); proxy != nil {
					subscription.Proxies = append(subscription.Proxies, proxy)
				}
			case "ssr":
				if proxy := proxyFromSSRURI(line); proxy != nil {
					subscription.Proxies = append(subscription.Proxies, proxy)
				}
			}
		}
		if len(subscription.Proxies) > 0 {
			return subscription
		}
	}
	return subscription
}

func decodeBase64Subscription(content []byte) (string, bool) {
	compact := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\r', '\n':
			return -1
		default:
			return r
		}
	}, string(content))
	if compact == "" {
		return "", false
	}
	for _, encoding := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		decoded, err := encoding.DecodeString(compact)
		if err == nil && strings.Contains(string(decoded), "://") {
			return string(decoded), true
		}
	}
	return "", false
}

func proxyFromSSRURI(line string) map[string]any {
	_, body, ok := strings.Cut(strings.TrimSpace(line), "://")
	if !ok {
		return nil
	}
	decoded, ok := decodeSSRBase64(body)
	if !ok {
		return nil
	}
	before, after, ok := strings.Cut(decoded, "/?")
	if !ok {
		return nil
	}
	host, port, protocol, method, obfsName, passwordEncoded, ok := splitSSRMainFields(before)
	if !ok {
		return nil
	}
	password, ok := decodeSSRBase64(passwordEncoded)
	if !ok {
		return nil
	}
	query, err := url.ParseQuery(strings.NewReplacer("+", "-", "/", "_").Replace(after))
	if err != nil {
		return nil
	}
	name := decodeSSRQueryValue(query, "remarks")
	if name == "" {
		name = host
	}
	proxy := map[string]any{
		"name":     name,
		"type":     "ssr",
		"server":   host,
		"port":     port,
		"cipher":   method,
		"password": password,
		"obfs":     obfsName,
		"protocol": protocol,
		"udp":      true,
	}
	if obfsParam := decodeSSRQueryValue(query, "obfsparam"); obfsParam != "" {
		proxy["obfs-param"] = obfsParam
	}
	if protocolParam := decodeSSRQueryValue(query, "protoparam"); protocolParam != "" {
		proxy["protocol-param"] = protocolParam
	}
	return proxy
}

func splitSSRMainFields(before string) (host string, port string, protocol string, method string, obfsName string, password string, ok bool) {
	parts := make([]string, 0, 5)
	remaining := before
	for len(parts) < 5 {
		separator := strings.LastIndex(remaining, ":")
		if separator < 0 {
			return
		}
		parts = append(parts, remaining[separator+1:])
		remaining = remaining[:separator]
	}
	host = strings.TrimPrefix(strings.TrimSuffix(remaining, "]"), "[")
	port = parts[4]
	protocol = parts[3]
	method = parts[2]
	obfsName = parts[1]
	password = parts[0]
	ok = host != "" && port != "" && protocol != "" && method != "" && obfsName != "" && password != ""
	return
}

func decodeSSRQueryValue(query url.Values, key string) string {
	value := query.Get(key)
	if value == "" {
		return ""
	}
	decoded, ok := decodeSSRBase64(value)
	if !ok {
		return ""
	}
	return decoded
}

func decodeSSRBase64(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	if unescaped, err := url.PathUnescape(value); err == nil {
		value = unescaped
	}
	candidates := []string{value}
	if standard := strings.NewReplacer("-", "+", "_", "/").Replace(value); standard != value {
		candidates = append(candidates, standard)
	}
	if urlSafe := strings.NewReplacer("+", "-", "/", "_").Replace(value); urlSafe != value {
		candidates = append(candidates, urlSafe)
	}
	for _, candidate := range candidates {
		trimmed := strings.TrimRight(candidate, "=")
		padded := candidate
		if padding := len(padded) % 4; padding != 0 {
			padded += strings.Repeat("=", 4-padding)
		}
		for _, item := range []struct {
			encoding *base64.Encoding
			value    string
		}{
			{base64.StdEncoding, padded},
			{base64.URLEncoding, padded},
			{base64.RawStdEncoding, trimmed},
			{base64.RawURLEncoding, trimmed},
		} {
			decoded, err := item.encoding.DecodeString(item.value)
			if err == nil {
				return string(decoded), true
			}
		}
	}
	return "", false
}

func proxyFromTUICURI(uri *url.URL) map[string]any {
	if uri.Hostname() == "" {
		return nil
	}
	name := uri.Fragment
	if decodedName, err := url.QueryUnescape(name); err == nil {
		name = decodedName
	}
	if name == "" {
		name = uri.Hostname()
	}
	proxy := map[string]any{
		"name":   name,
		"type":   "tuic",
		"server": uri.Hostname(),
		"port":   uri.Port(),
		"uuid":   uri.User.Username(),
	}
	if password, loaded := uri.User.Password(); loaded {
		proxy["password"] = password
	}
	query := uri.Query()
	copyQueryValue(proxy, query, "sni", "sni")
	copyQueryValue(proxy, query, "alpn", "alpn")
	copyQueryValue(proxy, query, "fp", "client-fingerprint")
	copyQueryValue(proxy, query, "congestion_control", "congestion_control")
	copyQueryValue(proxy, query, "udp_relay_mode", "udp_relay_mode")
	copyQueryValue(proxy, query, "reduce_rtt", "reduce_rtt")
	copyQueryValue(proxy, query, "udp", "udp")
	copyQueryValue(proxy, query, "tfo", "tfo")
	copyQueryValue(proxy, query, "allow_insecure", "skip-cert-verify")
	copyQueryValue(proxy, query, "insecure", "skip-cert-verify")
	copyQueryValue(proxy, query, "disable_sni", "disable_sni")
	return proxy
}

func copyQueryValue(proxy map[string]any, query url.Values, queryKey string, proxyKey string) {
	value := query.Get(queryKey)
	if value == "" {
		return
	}
	if proxyKey == "alpn" {
		values := strings.Split(value, ",")
		for index := range values {
			values[index] = strings.TrimSpace(values[index])
		}
		proxy[proxyKey] = values
		return
	}
	proxy[proxyKey] = value
}

func pruneMissingGroupDependencies(logger log.ContextLogger, options *option.Options, hasUnavailableProvider bool) {
	availableTags := existingOutboundTags(options)
	var emptyGroupFallbackTag string
	ensureEmptyGroupFallback := func() string {
		if emptyGroupFallbackTag == "" {
			emptyGroupFallbackTag = addEmptyGroupFallback(options, availableTags)
		}
		return emptyGroupFallbackTag
	}
	for i := range options.Outbounds {
		switch options.Outbounds[i].Type {
		case "selector":
			groupOptions, ok := options.Outbounds[i].Options.(*option.SelectorOutboundOptions)
			if !ok {
				continue
			}
			groupOptions.Outbounds = pruneMissingOutbounds(logger, options.Outbounds[i].Tag, groupOptions.Outbounds, availableTags, hasUnavailableProvider)
			if len(groupOptions.Outbounds) == 0 {
				fallbackTag := ensureEmptyGroupFallback()
				logger.Warn("outbound group ", options.Outbounds[i].Tag, " has no members after proxy-provider expansion, using block fallback: ", fallbackTag)
				groupOptions.Outbounds = []string{fallbackTag}
			}
			if groupOptions.Default != "" && !availableTags[groupOptions.Default] {
				logger.Warn("outbound group ", options.Outbounds[i].Tag, " default outbound unavailable, clearing: ", groupOptions.Default)
				groupOptions.Default = ""
			}
		case "urltest":
			groupOptions, ok := options.Outbounds[i].Options.(*option.URLTestOutboundOptions)
			if !ok {
				continue
			}
			groupOptions.Outbounds = pruneMissingOutbounds(logger, options.Outbounds[i].Tag, groupOptions.Outbounds, availableTags, hasUnavailableProvider)
			if len(groupOptions.Outbounds) == 0 {
				fallbackTag := ensureEmptyGroupFallback()
				logger.Warn("outbound group ", options.Outbounds[i].Tag, " has no members after proxy-provider expansion, using block fallback: ", fallbackTag)
				groupOptions.Outbounds = []string{fallbackTag}
			}
		}
	}
}

func addEmptyGroupFallback(options *option.Options, availableTags map[string]bool) string {
	const fallbackBaseTag = "empty-outbound-group"
	fallbackTag := fallbackBaseTag
	for index := 1; availableTags[fallbackTag]; index++ {
		fallbackTag = fallbackBaseTag + "-" + stringIndex(index)
	}
	options.Outbounds = append(options.Outbounds, option.Outbound{
		Type:    C.TypeBlock,
		Tag:     fallbackTag,
		Options: &option.StubOptions{},
	})
	availableTags[fallbackTag] = true
	return fallbackTag
}

func pruneMissingOutbounds(logger log.ContextLogger, groupTag string, outbounds []string, availableTags map[string]bool, hasUnavailableProvider bool) []string {
	if len(outbounds) == 0 {
		return outbounds
	}
	pruned := outbounds[:0]
	for _, tag := range outbounds {
		if availableTags[tag] {
			pruned = append(pruned, tag)
			continue
		}
		if hasUnavailableProvider {
			logger.Warn("outbound group ", groupTag, " member unavailable, skipping: ", tag)
		} else {
			logger.Warn("outbound group ", groupTag, " member not found, skipping: ", tag)
		}
	}
	return pruned
}

func loadProviderContent(ctx context.Context, logger log.ContextLogger, name string, provider option.ProxyProvider) (providerContent, error) {
	switch strings.ToLower(provider.Type) {
	case "file":
		if provider.Path == "" {
			return providerContent{}, E.New("missing path")
		}
		content, err := os.ReadFile(filemanager.BasePath(ctx, provider.Path))
		return providerContent{content: content, source: providerContentSourceFile}, err
	case "http":
		if provider.URL == "" {
			return providerContent{}, E.New("missing url")
		}
		cachePath := provider.Path
		if cachePath == "" {
			cachePath = filepath.Join("proxy_providers", safeFileName(name)+".yaml")
		}
		cachePath = filemanager.BasePath(ctx, cachePath)
		if provider.Interval > 0 {
			if stat, err := os.Stat(cachePath); err == nil && time.Since(stat.ModTime()) < time.Duration(provider.Interval)*time.Second {
				if cached, readErr := os.ReadFile(cachePath); readErr == nil {
					return providerContent{content: cached, cachePath: cachePath, source: providerContentSourceCache}, nil
				} else {
					logger.Warn("read fresh proxy-provider ", name, " cache: ", readErr)
				}
			}
		}
		content, err := fetchProviderContent(ctx, provider)
		if err != nil {
			if cached, readErr := os.ReadFile(cachePath); readErr == nil {
				logger.Warn("fetch proxy-provider ", name, " failed, using cached content: ", err)
				return providerContent{content: cached, cachePath: cachePath, source: providerContentSourceCache}, nil
			}
			return providerContent{}, providerContentUnavailable{err: err}
		}
		return providerContent{content: content, cachePath: cachePath, source: providerContentSourceFetched}, nil
	default:
		return providerContent{}, E.New("unsupported provider type: ", provider.Type)
	}
}

func fetchProviderContent(ctx context.Context, provider option.ProxyProvider) ([]byte, error) {
	providerProxy := strings.TrimSpace(provider.Proxy)
	if providerProxy != "" && !isDirectProviderProxy(providerProxy) {
		return nil, E.New("proxy-provider proxy detour is not supported yet: ", providerProxy)
	}
	return fetchProvider(ctx, provider)
}

func fetchProvider(ctx context.Context, provider option.ProxyProvider) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, provider.URL, nil)
	if err != nil {
		return nil, err
	}
	for name, values := range provider.Header {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	client := &http.Client{Timeout: 30 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, E.New("unexpected status: ", response.Status)
	}
	return readAllLimited(response.Body, 16<<20)
}

func saveProviderCache(logger log.ContextLogger, name string, cachePath string, content []byte) {
	if cachePath == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err == nil {
		if writeErr := os.WriteFile(cachePath, content, 0o644); writeErr != nil {
			logger.Warn("save proxy-provider ", name, " cache: ", writeErr)
		}
	} else {
		logger.Warn("create proxy-provider ", name, " cache directory: ", err)
	}
}

func compileProviderFilter(pattern string) ([]*regexp.Regexp, error) {
	if pattern == "" {
		return nil, nil
	}
	parts := strings.Split(pattern, "`")
	filters := make([]*regexp.Regexp, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		compiled, err := regexp.Compile(part)
		if err != nil {
			return nil, err
		}
		filters = append(filters, compiled)
	}
	return filters, nil
}

func matchFilter(filters []*regexp.Regexp, value string, emptyMatch bool) bool {
	if len(filters) == 0 {
		return emptyMatch
	}
	for _, filter := range filters {
		if filter.MatchString(value) {
			return true
		}
	}
	return false
}

func matchProxyNameFilter(filters []*regexp.Regexp, proxy resolvedProxy, emptyMatch bool) bool {
	return matchFilterValues(filters, emptyMatch, proxy.name, proxy.tag)
}

func matchProxyNameExcludeFilter(filters []*regexp.Regexp, proxy resolvedProxy) bool {
	return matchFilterValues(filters, false, proxy.name, proxy.tag, proxy.providerName)
}

func matchFilterValues(filters []*regexp.Regexp, emptyMatch bool, values ...string) bool {
	if len(filters) == 0 {
		return emptyMatch
	}
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		if matchFilter(filters, value, false) {
			return true
		}
	}
	return false
}

func matchProxyType(filters []*regexp.Regexp, proxy map[string]any) bool {
	return matchProxyTypeName(filters, stringValue(proxy, "type"))
}

func matchProxyTypeName(filters []*regexp.Regexp, proxyType string) bool {
	if len(filters) == 0 {
		return false
	}
	if matchFilter(filters, proxyType, false) {
		return true
	}
	normalizedType := proxyTypeName(proxyType)
	return normalizedType != proxyType && matchFilter(filters, normalizedType, false)
}

func proxyTypeName(proxyType string) string {
	switch strings.ToLower(proxyType) {
	case "http", "https":
		return "HTTP"
	case "socks", "socks5", "socks5h":
		return "SOCKS"
	case "ss", "shadowsocks":
		return "Shadowsocks"
	case "snell":
		return "Snell"
	case "vless":
		return "VLESS"
	case "vmess":
		return "VMess"
	case "trojan":
		return "Trojan"
	case "hysteria":
		return "Hysteria"
	case "hy2", "hysteria2":
		return "Hysteria2"
	case "tuic":
		return "TUIC"
	case "wireguard":
		return "WireGuard"
	case "ssh":
		return "SSH"
	case "anytls":
		return "AnyTLS"
	default:
		return proxyType
	}
}

func isDirectProviderProxy(proxy string) bool {
	return strings.EqualFold(proxy, "direct")
}

func safeFileName(name string) string {
	replacer := strings.NewReplacer("\\", "_", "/", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_")
	name = replacer.Replace(name)
	if name == "" {
		return "provider"
	}
	return name
}

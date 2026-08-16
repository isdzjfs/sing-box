package proxyprovider

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
)

// UpdateHTTPProviders performs the user-triggered, one-shot provider update used while no Box is
// running. It only commits content after the complete candidate snapshot has been parsed, keeping
// config validation and Box construction strictly offline.
func UpdateHTTPProviders(
	ctx context.Context,
	logger log.ContextLogger,
	options *option.Options,
	transport http.RoundTripper,
) (int, error) {
	return updateHTTPProviders(ctx, logger, options, transport, false)
}

// UpdateMissingHTTPProviders downloads only providers that have no usable cache. Android profile
// import uses this as a distinct preparation phase before its otherwise-offline validation.
func UpdateMissingHTTPProviders(
	ctx context.Context,
	logger log.ContextLogger,
	options *option.Options,
	transport http.RoundTripper,
) (int, error) {
	return updateHTTPProviders(ctx, logger, options, transport, true)
}

func updateHTTPProviders(
	ctx context.Context,
	logger log.ContextLogger,
	options *option.Options,
	transport http.RoundTripper,
	missingOnly bool,
) (int, error) {
	runtime, err := ExpandRuntime(ctx, logger, options)
	if err != nil {
		return 0, err
	}
	if runtime == nil {
		return 0, nil
	}
	if transport == nil {
		return 0, E.New("missing HTTP transport")
	}
	manager := &Manager{
		ctx:      ctx,
		logger:   logger,
		runtime:  runtime,
		contents: make(map[string]providerContent),
	}
	for name, provider := range runtime.resolved {
		if len(provider.content.content) > 0 {
			manager.contents[name] = provider.content
		}
	}
	candidateContents := make(map[string]providerContent, len(manager.contents))
	for name, content := range manager.contents {
		candidateContents[name] = content
	}

	type pendingWrite struct {
		name         string
		content      providerContent
		writeContent bool
	}
	pending := make([]pendingWrite, 0, len(runtime.providers))
	for _, name := range sortedProviderNames(runtime.providers) {
		provider := runtime.providers[name]
		if !strings.EqualFold(provider.Type, "http") {
			continue
		}
		previous := candidateContents[name]
		if missingOnly && len(previous.content) > 0 {
			continue
		}
		providerProxy := strings.TrimSpace(provider.Proxy)
		if providerProxy != "" && !isDirectProviderProxy(providerProxy) {
			return 0, E.New(
				"proxy-provider ", name,
				" requires download proxy ", providerProxy,
				"; update it while the service is running",
			)
		}
		fetched, fetchErr := fetchProvider(ctx, provider, previous, transport)
		if fetchErr != nil {
			return 0, E.Cause(sanitizeProviderError(fetchErr), "update proxy-provider ", name)
		}
		if fetched.notModified {
			if len(previous.content) == 0 {
				return 0, E.New("proxy-provider ", name, " returned not modified without cached content")
			}
			previous.modTime = time.Now()
			if fetched.etag != "" {
				previous.etag = fetched.etag
			}
			if fetched.lastModified != "" {
				previous.modified = fetched.lastModified
			}
			candidateContents[name] = previous
			pending = append(pending, pendingWrite{name: name, content: previous})
			continue
		}
		content := providerContent{
			content:     fetched.content,
			cachePath:   providerCachePath(ctx, name, provider),
			source:      providerContentSourceCache,
			modTime:     time.Now(),
			etag:        fetched.etag,
			modified:    fetched.lastModified,
			fingerprint: providerSourceFingerprint(provider),
		}
		candidateContents[name] = content
		pending = append(pending, pendingWrite{name: name, content: content, writeContent: true})
	}
	if len(pending) == 0 {
		return 0, nil
	}
	resolved, err := manager.resolveContents(candidateContents)
	if err != nil {
		return 0, err
	}
	if _, err = manager.resolveGroupMembers(resolved); err != nil {
		return 0, err
	}
	for _, update := range pending {
		if update.writeContent {
			if err = saveProviderCacheAtomic(update.content.cachePath, update.content.content); err != nil {
				return 0, E.Cause(err, "save proxy-provider ", update.name, " cache")
			}
		}
		if err = saveProviderMetadata(update.content.cachePath, update.content); err != nil {
			return 0, E.Cause(err, "save proxy-provider ", update.name, " metadata")
		}
	}
	return len(pending), nil
}

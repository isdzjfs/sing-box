package box_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"

	"github.com/stretchr/testify/require"
)

func TestProxyProviderUnavailablePrunesMissingGroupDependencyAtStart(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(include.Context(context.Background()))
	instance, err := box.New(box.Options{
		Context: ctx,
		Options: option.Options{
			ProxyProviders: map[string]option.ProxyProvider{
				"WestData": {
					Type: "http",
					URL:  server.URL,
					Path: filepath.Join(t.TempDir(), "missing.yaml"),
				},
			},
			Outbounds: []option.Outbound{
				{Type: C.TypeDirect, Tag: "DIRECT"},
				{
					Type: C.TypeSelector,
					Tag:  "Apple",
					Options: &option.SelectorOutboundOptions{
						Outbounds: []string{"DIRECT", "低倍率"},
						Default:   "DIRECT",
					},
				},
			},
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		instance.Close()
		cancel()
	})

	require.NoError(t, instance.Start())
}

func TestProxyProviderExpansionPrunesMissingGroupDependencyAtStart(t *testing.T) {
	subscriptionPath := filepath.Join(t.TempDir(), "subscription.yaml")
	require.NoError(t, os.WriteFile(subscriptionPath, []byte(`
proxies:
  - name: HK SS
    type: ss
    server: hk.example.com
    port: 8388
    cipher: aes-128-gcm
    password: hk-pass
`), 0o644))

	ctx, cancel := context.WithCancel(include.Context(context.Background()))
	instance, err := box.New(box.Options{
		Context: ctx,
		Options: option.Options{
			ProxyProviders: map[string]option.ProxyProvider{
				"sub": {
					Type: "file",
					Path: subscriptionPath,
				},
			},
			Outbounds: []option.Outbound{
				{Type: C.TypeDirect, Tag: "DIRECT"},
				{
					Type: C.TypeSelector,
					Tag:  "Apple",
					Options: &option.SelectorOutboundOptions{
						Outbounds: []string{"DIRECT", "低倍率"},
						Default:   "DIRECT",
					},
				},
			},
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		instance.Close()
		cancel()
	})

	require.NoError(t, instance.Start())
}

func TestHTTPProxyProviderRefreshesAfterStartWithoutBlockingConstruction(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		_, _ = w.Write([]byte("\nproxies:\n  - name: Remote SS\n    type: ss\n    server: remote.example.com\n    port: 8388\n    cipher: aes-128-gcm\n    password: remote-pass\n"))
	}))
	defer server.Close()

	cachePath := filepath.Join(t.TempDir(), "subscription.yaml")
	ctx, cancel := context.WithCancel(include.Context(context.Background()))
	constructedAt := time.Now()
	instance, err := box.New(box.Options{
		Context: ctx,
		Options: option.Options{
			ProxyProviders: map[string]option.ProxyProvider{
				"sub": {
					Type:     "http",
					URL:      server.URL,
					Path:     cachePath,
					Interval: 3600,
				},
			},
			Outbounds: []option.Outbound{
				{
					Type: C.TypeSelector,
					Tag:  "Proxy",
					Options: &option.SelectorOutboundOptions{
						Use: []string{"sub"},
					},
				},
			},
		},
	})
	require.NoError(t, err)
	require.Less(t, time.Since(constructedAt), time.Second)
	require.Zero(t, requestCount.Load(), "box construction must not fetch providers")
	t.Cleanup(func() {
		_ = instance.Close()
		cancel()
	})

	startedAt := time.Now()
	require.NoError(t, instance.Start())
	require.Less(t, time.Since(startedAt), time.Second)

	outbound, loaded := instance.Outbound().Outbound("Proxy")
	require.True(t, loaded)
	group := outbound.(adapter.OutboundGroup)
	require.Equal(t, []string{"empty-outbound-group"}, group.All())
	require.Eventually(t, func() bool {
		_, cacheErr := os.Stat(cachePath)
		return requestCount.Load() == 1 && slices.Equal(group.All(), []string{"Remote SS"}) && cacheErr == nil
	}, 5*time.Second, 20*time.Millisecond)
	cached, err := os.ReadFile(cachePath)
	require.NoError(t, err)
	require.Contains(t, string(cached), "Remote SS")
}

func TestStaleHTTPProxyProviderCacheStartsImmediatelyThenRefreshes(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		_, _ = w.Write([]byte("\nproxies:\n  - name: New SS\n    type: ss\n    server: new.example.com\n    port: 8388\n    cipher: aes-128-gcm\n    password: new-pass\n"))
	}))
	defer server.Close()

	cachePath := filepath.Join(t.TempDir(), "subscription.yaml")
	require.NoError(t, os.WriteFile(cachePath, []byte("\nproxies:\n  - name: Cached SS\n    type: ss\n    server: cached.example.com\n    port: 8388\n    cipher: aes-128-gcm\n    password: cached-pass\n"), 0o600))
	staleTime := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(cachePath, staleTime, staleTime))

	ctx, cancel := context.WithCancel(include.Context(context.Background()))
	instance, err := box.New(box.Options{
		Context: ctx,
		Options: option.Options{
			ProxyProviders: map[string]option.ProxyProvider{
				"sub": {
					Type:     "http",
					URL:      server.URL,
					Path:     cachePath,
					Interval: 1,
				},
			},
			Outbounds: []option.Outbound{
				{
					Type: C.TypeSelector,
					Tag:  "Proxy",
					Options: &option.SelectorOutboundOptions{
						Use: []string{"sub"},
					},
				},
			},
		},
	})
	require.NoError(t, err)
	require.Zero(t, requestCount.Load(), "stale cache must not trigger construction-time fetch")
	t.Cleanup(func() {
		_ = instance.Close()
		cancel()
	})

	outbound, loaded := instance.Outbound().Outbound("Proxy")
	require.True(t, loaded)
	group := outbound.(adapter.OutboundGroup)
	require.Equal(t, []string{"Cached SS"}, group.All())

	startedAt := time.Now()
	require.NoError(t, instance.Start())
	require.Less(t, time.Since(startedAt), time.Second)
	require.Equal(t, []string{"Cached SS"}, group.All())
	require.Eventually(t, func() bool {
		return requestCount.Load() == 1 && slices.Equal(group.All(), []string{"New SS"})
	}, 5*time.Second, 20*time.Millisecond)
	_, oldLoaded := instance.Outbound().Outbound("Cached SS")
	require.False(t, oldLoaded)
	_, newLoaded := instance.Outbound().Outbound("New SS")
	require.True(t, newLoaded)
}

func TestInvalidHTTPProxyProviderRefreshKeepsLastKnownGoodSnapshot(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		_, _ = w.Write([]byte("<html>login required</html>"))
	}))
	defer server.Close()

	cachePath := filepath.Join(t.TempDir(), "subscription.yaml")
	cachedContent := "\nproxies:\n  - name: Cached SS\n    type: ss\n    server: cached.example.com\n    port: 8388\n    cipher: aes-128-gcm\n    password: cached-pass\n"
	require.NoError(t, os.WriteFile(cachePath, []byte(cachedContent), 0o600))
	staleTime := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(cachePath, staleTime, staleTime))

	ctx, cancel := context.WithCancel(include.Context(context.Background()))
	instance, err := box.New(box.Options{
		Context: ctx,
		Options: option.Options{
			ProxyProviders: map[string]option.ProxyProvider{
				"sub": {
					Type:     "http",
					URL:      server.URL,
					Path:     cachePath,
					Interval: 1,
				},
			},
			Outbounds: []option.Outbound{
				{
					Type: C.TypeSelector,
					Tag:  "Proxy",
					Options: &option.SelectorOutboundOptions{
						Use: []string{"sub"},
					},
				},
			},
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = instance.Close()
		cancel()
	})
	require.NoError(t, instance.Start())

	outbound, loaded := instance.Outbound().Outbound("Proxy")
	require.True(t, loaded)
	group := outbound.(adapter.OutboundGroup)
	require.Eventually(t, func() bool {
		return requestCount.Load() == 1
	}, 5*time.Second, 20*time.Millisecond)
	require.Equal(t, []string{"Cached SS"}, group.All())
	cached, err := os.ReadFile(cachePath)
	require.NoError(t, err)
	require.Equal(t, cachedContent, string(cached))
}

func TestFreshHTTPProxyProviderCacheDoesNotRefreshOnStart(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		http.Error(w, "unexpected refresh", http.StatusInternalServerError)
	}))
	defer server.Close()

	cachePath := filepath.Join(t.TempDir(), "subscription.yaml")
	require.NoError(t, os.WriteFile(cachePath, []byte("\nproxies:\n  - name: Cached SS\n    type: ss\n    server: cached.example.com\n    port: 8388\n    cipher: aes-128-gcm\n    password: cached-pass\n"), 0o600))
	ctx, cancel := context.WithCancel(include.Context(context.Background()))
	instance, err := box.New(box.Options{
		Context: ctx,
		Options: option.Options{
			ProxyProviders: map[string]option.ProxyProvider{
				"sub": {
					Type:     "http",
					URL:      server.URL,
					Path:     cachePath,
					Interval: 3600,
				},
			},
			Outbounds: []option.Outbound{
				{Type: C.TypeSelector, Tag: "Proxy", Options: &option.SelectorOutboundOptions{Use: []string{"sub"}}},
			},
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = instance.Close()
		cancel()
	})
	require.NoError(t, instance.Start())
	time.Sleep(250 * time.Millisecond)
	require.Zero(t, requestCount.Load())
}

func TestHTTPProxyProviderNotModifiedKeepsSnapshotAndUsesETag(t *testing.T) {
	var requestCount atomic.Int32
	var ifNoneMatch atomic.Value
	ifNoneMatch.Store("")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		ifNoneMatch.Store(r.Header.Get("If-None-Match"))
		w.Header().Set("ETag", `"v1"`)
		w.WriteHeader(http.StatusNotModified)
	}))
	defer server.Close()

	cachePath := filepath.Join(t.TempDir(), "subscription.yaml")
	cachedContent := "\nproxies:\n  - name: Cached SS\n    type: ss\n    server: cached.example.com\n    port: 8388\n    cipher: aes-128-gcm\n    password: cached-pass\n"
	require.NoError(t, os.WriteFile(cachePath, []byte(cachedContent), 0o600))
	staleTime := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(cachePath, staleTime, staleTime))
	metadata, err := json.Marshal(map[string]any{
		"etag":       `"v1"`,
		"updated_at": staleTime,
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cachePath+".meta.json", metadata, 0o600))

	ctx, cancel := context.WithCancel(include.Context(context.Background()))
	instance, err := box.New(box.Options{
		Context: ctx,
		Options: option.Options{
			ProxyProviders: map[string]option.ProxyProvider{
				"sub": {
					Type:     "http",
					URL:      server.URL,
					Path:     cachePath,
					Interval: 3600,
				},
			},
			Outbounds: []option.Outbound{
				{Type: C.TypeSelector, Tag: "Proxy", Options: &option.SelectorOutboundOptions{Use: []string{"sub"}}},
			},
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = instance.Close()
		cancel()
	})
	require.Zero(t, requestCount.Load())
	require.NoError(t, instance.Start())
	require.Eventually(t, func() bool {
		return requestCount.Load() == 1
	}, 5*time.Second, 20*time.Millisecond)
	require.Equal(t, `"v1"`, ifNoneMatch.Load())

	outbound, loaded := instance.Outbound().Outbound("Proxy")
	require.True(t, loaded)
	require.Equal(t, []string{"Cached SS"}, outbound.(adapter.OutboundGroup).All())
	cached, err := os.ReadFile(cachePath)
	require.NoError(t, err)
	require.Equal(t, cachedContent, string(cached))
}

func TestHTTPProxyProviderDownloadProxySelfCycleIsRejected(t *testing.T) {
	ctx, cancel := context.WithCancel(include.Context(context.Background()))
	defer cancel()
	instance, err := box.New(box.Options{
		Context: ctx,
		Options: option.Options{
			ProxyProviders: map[string]option.ProxyProvider{
				"sub": {
					Type:  "http",
					URL:   "https://provider.example/subscription",
					Path:  filepath.Join(t.TempDir(), "missing.yaml"),
					Proxy: "Proxy",
				},
			},
			Outbounds: []option.Outbound{
				{Type: C.TypeSelector, Tag: "Proxy", Options: &option.SelectorOutboundOptions{Use: []string{"sub"}}},
			},
		},
	})
	if instance != nil {
		_ = instance.Close()
	}
	require.ErrorContains(t, err, "depends on the provider itself")
}

func TestHTTPProxyProviderMissingDownloadProxyIsRejected(t *testing.T) {
	ctx, cancel := context.WithCancel(include.Context(context.Background()))
	defer cancel()
	instance, err := box.New(box.Options{
		Context: ctx,
		Options: option.Options{
			ProxyProviders: map[string]option.ProxyProvider{
				"sub": {
					Type:  "http",
					URL:   "https://provider.example/subscription",
					Path:  filepath.Join(t.TempDir(), "missing.yaml"),
					Proxy: "missing-outbound",
				},
			},
			Outbounds: []option.Outbound{
				{Type: C.TypeDirect, Tag: "DIRECT"},
			},
		},
	})
	if instance != nil {
		_ = instance.Close()
	}
	require.ErrorContains(t, err, "download proxy not found")
}

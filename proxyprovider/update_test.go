package proxyprovider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"
	"github.com/stretchr/testify/require"
)

const updateTestSubscription = `
proxies:
  - name: Updated SS
    type: ss
    server: updated.example.com
    port: 8388
    cipher: aes-128-gcm
    password: updated-pass
`

func TestUpdateHTTPProvidersWritesValidatedCacheAndUsesConditionalRequest(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests++
		require.Equal(t, "SenVPN", request.Header.Get("User-Agent"))
		if requests == 2 {
			require.Equal(t, `"provider-v1"`, request.Header.Get("If-None-Match"))
			response.WriteHeader(http.StatusNotModified)
			return
		}
		response.Header().Set("ETag", `"provider-v1"`)
		_, _ = response.Write([]byte(updateTestSubscription))
	}))
	defer server.Close()

	cachePath := filepath.Join(t.TempDir(), "provider.yaml")
	newOptions := func() option.Options {
		return option.Options{ProxyProviders: map[string]option.ProxyProvider{
			"sub": {
				Type:   "http",
				URL:    server.URL,
				Path:   cachePath,
				Header: badoption.HTTPHeader{"User-Agent": []string{"SenVPN"}},
			},
		}}
	}
	options := newOptions()
	updated, err := UpdateHTTPProviders(
		context.Background(),
		log.NewNOPFactory().Logger(),
		&options,
		http.DefaultTransport,
	)
	require.NoError(t, err)
	require.Equal(t, 1, updated)
	require.FileExists(t, providerMetadataPath(cachePath))
	require.Equal(t, updateTestSubscription, string(requireReadFile(t, cachePath)))

	options = newOptions()
	updated, err = UpdateHTTPProviders(
		context.Background(),
		log.NewNOPFactory().Logger(),
		&options,
		http.DefaultTransport,
	)
	require.NoError(t, err)
	require.Equal(t, 1, updated)
	require.Equal(t, 2, requests)

	resolved := newOptions()
	require.NoError(t, Expand(context.Background(), log.NewNOPFactory().Logger(), &resolved))
	require.Len(t, resolved.Outbounds, 1)
	require.Equal(t, "Updated SS", resolved.Outbounds[0].Tag)
}

func TestUpdateHTTPProvidersKeepsLastGoodCacheWhenNewContentIsInvalid(t *testing.T) {
	content := updateTestSubscription
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(content))
	}))
	defer server.Close()
	cachePath := filepath.Join(t.TempDir(), "provider.yaml")
	newOptions := func() option.Options {
		return option.Options{ProxyProviders: map[string]option.ProxyProvider{
			"sub": {Type: "http", URL: server.URL, Path: cachePath},
		}}
	}

	options := newOptions()
	_, err := UpdateHTTPProviders(context.Background(), log.NewNOPFactory().Logger(), &options, http.DefaultTransport)
	require.NoError(t, err)
	lastGood := requireReadFile(t, cachePath)

	content = "proxies:\n  - name: broken\n    type: unsupported\n"
	options = newOptions()
	_, err = UpdateHTTPProviders(context.Background(), log.NewNOPFactory().Logger(), &options, http.DefaultTransport)
	require.Error(t, err)
	require.Equal(t, lastGood, requireReadFile(t, cachePath))
}

func TestUpdateMissingHTTPProvidersFetchesOnlyWithoutUsableCache(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = response.Write([]byte(updateTestSubscription))
	}))
	defer server.Close()
	cachePath := filepath.Join(t.TempDir(), "provider.yaml")
	newOptions := func() option.Options {
		return option.Options{ProxyProviders: map[string]option.ProxyProvider{
			"sub": {Type: "http", URL: server.URL, Path: cachePath},
		}}
	}

	options := newOptions()
	updated, err := UpdateMissingHTTPProviders(
		context.Background(),
		log.NewNOPFactory().Logger(),
		&options,
		http.DefaultTransport,
	)
	require.NoError(t, err)
	require.Equal(t, 1, updated)
	require.Equal(t, 1, requests)

	options = newOptions()
	updated, err = UpdateMissingHTTPProviders(
		context.Background(),
		log.NewNOPFactory().Logger(),
		&options,
		http.DefaultTransport,
	)
	require.NoError(t, err)
	require.Zero(t, updated)
	require.Equal(t, 1, requests)
}

func TestUpdateHTTPProvidersRejectsRuntimeDownloadProxy(t *testing.T) {
	options := option.Options{ProxyProviders: map[string]option.ProxyProvider{
		"sub": {
			Type:  "http",
			URL:   "https://provider.example/subscription?token=secret",
			Proxy: "proxy",
		},
	}}
	_, err := UpdateHTTPProviders(
		context.Background(),
		log.NewNOPFactory().Logger(),
		&options,
		http.DefaultTransport,
	)
	require.ErrorContains(t, err, "update it while the service is running")
	require.NotContains(t, err.Error(), "token=secret")
}

func requireReadFile(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	return content
}

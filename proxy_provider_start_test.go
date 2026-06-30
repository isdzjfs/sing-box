package box_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	box "github.com/sagernet/sing-box"
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

package proxyprovider

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/stretchr/testify/require"
)

func TestServerAddressFromOptions(t *testing.T) {
	address := serverAddressFromOptions(&option.ShadowsocksOutboundOptions{
		ServerOptions: option.ServerOptions{
			Server:     "provider.example.com",
			ServerPort: 8388,
		},
	})
	require.Equal(t, "provider.example.com", address.Fqdn)
	require.Equal(t, uint16(8388), address.Port)
}

func TestSanitizeProviderErrorRemovesURL(t *testing.T) {
	inner := errors.New("lookup failed")
	err := sanitizeProviderError(&url.Error{
		Op:  "Get",
		URL: "https://provider.example/sub?token=secret",
		Err: inner,
	})
	require.ErrorIs(t, err, inner)
	require.NotContains(t, err.Error(), "token=secret")
}

func TestProviderCacheMetadataRejectsDifferentSource(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "provider.yaml")
	require.NoError(t, os.WriteFile(cachePath, []byte("proxies:\n  - name: cached\n"), 0o600))
	first := option.ProxyProvider{
		Type: "http",
		URL:  "https://first.example/subscription",
		Path: cachePath,
	}
	content, err := loadProviderContent(context.Background(), log.NewNOPFactory().Logger(), "sub", first)
	require.NoError(t, err)
	require.NoError(t, saveProviderMetadata(cachePath, content))

	second := first
	second.URL = "https://second.example/subscription"
	_, err = loadProviderContent(context.Background(), log.NewNOPFactory().Logger(), "sub", second)
	require.ErrorContains(t, err, "different provider source")
}

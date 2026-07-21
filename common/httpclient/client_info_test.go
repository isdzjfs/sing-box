package httpclient

import (
	"testing"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/stretchr/testify/require"
)

func TestNewHTTPTransportInfoUsesSafeNormalizedFields(t *testing.T) {
	info := newHTTPTransportInfo("rule-set-client", option.HTTPClientOptions{
		DialerOptions: option.DialerOptions{Detour: "selected-node"},
	})

	require.Equal(t, "rule-set-client", info.Tag)
	require.Equal(t, "selected-node", info.Detour)
	require.Equal(t, C.TLSEngineGo, info.Engine)
	require.Equal(t, 2, info.Version)
	require.False(t, info.DefaultOutbound)
}

func TestNewHTTPTransportInfoPreservesExplicitEngineAndVersion(t *testing.T) {
	info := newHTTPTransportInfo("", option.HTTPClientOptions{
		Engine:          C.TLSEngineApple,
		Version:         3,
		DefaultOutbound: true,
	})

	require.Empty(t, info.Tag)
	require.Empty(t, info.Detour)
	require.Equal(t, C.TLSEngineApple, info.Engine)
	require.Equal(t, 3, info.Version)
	require.True(t, info.DefaultOutbound)
}

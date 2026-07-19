package clashapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/protocol/group"
	singJSON "github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/service"
	"github.com/stretchr/testify/require"
)

func TestProxyInfoExposesGroupIconWithoutDownloadingIt(t *testing.T) {
	var requestCount atomic.Int32
	iconServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		http.Error(w, "image unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(iconServer.Close)

	registry := outbound.NewRegistry()
	group.RegisterSelector(registry)
	ctx := service.ContextWith[option.OutboundOptionsRegistry](context.Background(), registry)
	rawOutbound, err := json.Marshal(map[string]any{
		"type":      "selector",
		"tag":       "Emby",
		"outbounds": []string{"proxy-a"},
		"icon":      iconServer.URL + "/icon.png",
	})
	require.NoError(t, err)
	var outboundOptions option.Outbound
	require.NoError(t, singJSON.UnmarshalContext(ctx, rawOutbound, &outboundOptions))
	detour, err := registry.CreateOutbound(ctx, nil, nil, outboundOptions.Tag, outboundOptions.Type, outboundOptions.Options)
	require.NoError(t, err)

	response, err := proxyInfo(&Server{urlTestHistory: urltest.NewHistoryStorage()}, detour).MarshalJSON()
	require.NoError(t, err)

	var info map[string]any
	require.NoError(t, json.Unmarshal(response, &info))
	require.Equal(t, iconServer.URL+"/icon.png", info["icon"])
	require.Zero(t, requestCount.Load())
}

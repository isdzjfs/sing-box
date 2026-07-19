package box_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	box "github.com/sagernet/sing-box"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	"github.com/stretchr/testify/require"
)

func TestUnavailableGroupIconDoesNotAffectStart(t *testing.T) {
	var requestCount atomic.Int32
	iconServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		http.Error(w, "image unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(iconServer.Close)

	ctx, cancel := context.WithCancel(include.Context(context.Background()))
	instance, err := box.New(box.Options{
		Context: ctx,
		Options: option.Options{
			Outbounds: []option.Outbound{
				{Type: C.TypeDirect, Tag: "DIRECT"},
				{
					Type: C.TypeSelector,
					Tag:  "Emby",
					Options: &option.SelectorOutboundOptions{
						Outbounds: []string{"DIRECT"},
						Icon:      iconServer.URL + "/icon.png",
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
	require.Zero(t, requestCount.Load())
}

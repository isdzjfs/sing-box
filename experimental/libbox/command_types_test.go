package libbox

import (
	"testing"

	"github.com/sagernet/sing-box/daemon"
	"github.com/stretchr/testify/require"
)

func TestOutboundGroupIteratorPreservesServerAddress(t *testing.T) {
	groups := outboundGroupIteratorFromGRPC(&daemon.Groups{
		Group: []*daemon.Group{
			{
				Tag:  "Proxy",
				Type: "selector",
				Items: []*daemon.GroupItem{
					{
						Tag:               "Provider Node",
						Type:              "shadowsocks",
						Server:            "provider.example.com",
						ServerPort:        8388,
						UrlTestTime:       123,
						UrlTestTimeMillis: 123456,
						UrlTestSequence:   789,
					},
				},
			},
		},
	})
	require.True(t, groups.HasNext())
	items := groups.Next().GetItems()
	require.True(t, items.HasNext())
	item := items.Next()
	require.Equal(t, "provider.example.com", item.Server)
	require.Equal(t, int32(8388), item.ServerPort)
	require.Equal(t, int64(123), item.URLTestTime)
	require.Equal(t, int64(123456), item.URLTestTimeMillis)
	require.Equal(t, int64(789), item.URLTestSequence)
}

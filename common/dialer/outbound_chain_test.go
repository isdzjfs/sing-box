package dialer

import (
	"testing"

	"github.com/sagernet/sing-box/adapter"
	N "github.com/sagernet/sing/common/network"
	"github.com/stretchr/testify/require"
)

type chainOutbound struct {
	adapter.Outbound
	tag string
}

func (o *chainOutbound) Tag() string { return o.tag }

type chainGroup struct{ chainOutbound }

func (g *chainGroup) Now() string                         { return "tcp-node" }
func (g *chainGroup) All() []string                       { return []string{"tcp-node", "udp-node"} }
func (g *chainGroup) NowForNetwork(network string) string { return network + "-node" }

type chainManager struct {
	adapter.OutboundManager
	items map[string]adapter.Outbound
}

func (m *chainManager) Outbound(tag string) (adapter.Outbound, bool) {
	outbound, loaded := m.items[tag]
	return outbound, loaded
}

func TestOutboundChainUsesNetworkSpecificSelection(t *testing.T) {
	manager := &chainManager{items: map[string]adapter.Outbound{
		"auto":     &chainGroup{chainOutbound{tag: "auto"}},
		"tcp-node": &chainOutbound{tag: "tcp-node"},
		"udp-node": &chainOutbound{tag: "udp-node"},
	}}
	dialer := &DefaultDialer{outboundManager: manager}
	for _, network := range []string{N.NetworkTCP, N.NetworkUDP} {
		require.Equal(t, []string{network + "-node", "auto"}, dialer.outboundChain("auto", network))
	}
	require.Empty(t, dialer.outboundChain(""))
}

package trafficcontrol

import (
	"testing"

	"github.com/sagernet/sing-box/adapter"
	N "github.com/sagernet/sing/common/network"
	"github.com/stretchr/testify/require"
)

type trackerOutbound struct {
	adapter.Outbound
	tag string
}

func (o *trackerOutbound) Tag() string  { return o.tag }
func (o *trackerOutbound) Type() string { return "test" }

type trackerGroup struct{ trackerOutbound }

func (g *trackerGroup) Now() string                         { return "tcp-node" }
func (g *trackerGroup) All() []string                       { return []string{"tcp-node", "udp-node"} }
func (g *trackerGroup) NowForNetwork(network string) string { return network + "-node" }

type trackerManager struct {
	adapter.OutboundManager
	items map[string]adapter.Outbound
}

func (m *trackerManager) Outbound(tag string) (adapter.Outbound, bool) {
	outbound, loaded := m.items[tag]
	return outbound, loaded
}

func TestTrackerMetadataUsesNetworkSpecificSelection(t *testing.T) {
	group := &trackerGroup{trackerOutbound{tag: "auto"}}
	manager := &Manager{outbound: &trackerManager{items: map[string]adapter.Outbound{
		"auto":     group,
		"tcp-node": &trackerOutbound{tag: "tcp-node"},
		"udp-node": &trackerOutbound{tag: "udp-node"},
	}}}
	for _, network := range []string{N.NetworkTCP, N.NetworkUDP} {
		metadata := manager.newTrackerMetadata(adapter.InboundContext{Network: network}, nil, group, nil, nil)
		require.Equal(t, []string{network + "-node", "auto"}, metadata.Chain)
		require.Equal(t, network+"-node", metadata.Outbound)
	}
}

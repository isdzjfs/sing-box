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

func TestTrackerMetadataUsesResolvedNetworkChain(t *testing.T) {
	group := &trackerOutbound{tag: "auto"}
	manager := &Manager{}
	for _, network := range []string{N.NetworkTCP, N.NetworkUDP} {
		leaf := &trackerOutbound{tag: network + "-node"}
		metadata := manager.newTrackerMetadata(adapter.InboundContext{
			Network:       network,
			OutboundChain: []adapter.Outbound{group, leaf},
		}, nil, group, nil, nil)
		require.Equal(t, []string{network + "-node", "auto"}, metadata.Chain)
		require.Equal(t, network+"-node", metadata.Outbound)
	}
}

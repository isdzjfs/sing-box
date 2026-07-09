package route

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-tun"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/stretchr/testify/require"
)

func TestPreMatchFlowStopsAtInterruptibleOutboundGroup(t *testing.T) {
	flowOutbound := &preMatchFlowTestOutbound{tag: "flow"}
	group := &preMatchFlowTestGroup{
		tag:                          "auto",
		selected:                     flowOutbound.Tag(),
		interruptExternalConnections: true,
	}
	router := &Router{
		logger: log.NewNOPFactory().NewLogger("router"),
		outbound: &preMatchFlowTestOutboundManager{
			defaultOutbound: group,
			outbounds: map[string]adapter.Outbound{
				group.Tag():        group,
				flowOutbound.Tag(): flowOutbound,
			},
		},
	}

	result := router.preMatchFlow(context.Background(), &adapter.InboundContext{
		Network:     N.NetworkTCP,
		Destination: M.SocksaddrFrom(netip.MustParseAddr("198.51.100.1"), 443),
	}, M.Socksaddr{}, nil, group.Tag())

	require.Equal(t, adapter.PreMatchContinue, result.Action)
}

func TestPreMatchFlowExpandsNonInterruptibleOutboundGroup(t *testing.T) {
	flowOutbound := &preMatchFlowTestOutbound{tag: "flow"}
	group := &preMatchFlowTestGroup{
		tag:      "auto",
		selected: flowOutbound.Tag(),
	}
	router := &Router{
		logger: log.NewNOPFactory().NewLogger("router"),
		outbound: &preMatchFlowTestOutboundManager{
			defaultOutbound: group,
			outbounds: map[string]adapter.Outbound{
				group.Tag():        group,
				flowOutbound.Tag(): flowOutbound,
			},
		},
	}

	result := router.preMatchFlow(context.Background(), &adapter.InboundContext{
		Network:     N.NetworkTCP,
		Destination: M.SocksaddrFrom(netip.MustParseAddr("198.51.100.1"), 443),
	}, M.Socksaddr{}, nil, group.Tag())

	require.Equal(t, adapter.PreMatchFlow, result.Action)
	require.Equal(t, flowOutbound, result.Outbound)
}

type preMatchFlowTestOutboundManager struct {
	defaultOutbound adapter.Outbound
	outbounds       map[string]adapter.Outbound
}

func (m *preMatchFlowTestOutboundManager) Start(stage adapter.StartStage) error {
	return nil
}

func (m *preMatchFlowTestOutboundManager) Close() error {
	return nil
}

func (m *preMatchFlowTestOutboundManager) Outbounds() []adapter.Outbound {
	outbounds := make([]adapter.Outbound, 0, len(m.outbounds))
	for _, outbound := range m.outbounds {
		outbounds = append(outbounds, outbound)
	}
	return outbounds
}

func (m *preMatchFlowTestOutboundManager) Outbound(tag string) (adapter.Outbound, bool) {
	outbound, loaded := m.outbounds[tag]
	return outbound, loaded
}

func (m *preMatchFlowTestOutboundManager) Default() adapter.Outbound {
	return m.defaultOutbound
}

func (m *preMatchFlowTestOutboundManager) Remove(tag string) error {
	return errors.New("not implemented")
}

func (m *preMatchFlowTestOutboundManager) Create(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, outboundType string, options any) error {
	return errors.New("not implemented")
}

type preMatchFlowTestGroup struct {
	tag                          string
	selected                     string
	interruptExternalConnections bool
}

func (g *preMatchFlowTestGroup) Type() string {
	return "test-group"
}

func (g *preMatchFlowTestGroup) Tag() string {
	return g.tag
}

func (g *preMatchFlowTestGroup) Network() []string {
	return []string{N.NetworkTCP, N.NetworkUDP}
}

func (g *preMatchFlowTestGroup) Dependencies() []string {
	return nil
}

func (g *preMatchFlowTestGroup) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	return nil, errors.New("not implemented")
}

func (g *preMatchFlowTestGroup) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("not implemented")
}

func (g *preMatchFlowTestGroup) Now() string {
	return g.selected
}

func (g *preMatchFlowTestGroup) All() []string {
	return []string{g.selected}
}

func (g *preMatchFlowTestGroup) InterruptsExternalConnections() bool {
	return g.interruptExternalConnections
}

type preMatchFlowTestOutbound struct {
	tag string
}

func (o *preMatchFlowTestOutbound) Type() string {
	return "test-flow"
}

func (o *preMatchFlowTestOutbound) Tag() string {
	return o.tag
}

func (o *preMatchFlowTestOutbound) Network() []string {
	return []string{N.NetworkTCP, N.NetworkUDP}
}

func (o *preMatchFlowTestOutbound) Dependencies() []string {
	return nil
}

func (o *preMatchFlowTestOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	return nil, errors.New("not implemented")
}

func (o *preMatchFlowTestOutbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("not implemented")
}

func (o *preMatchFlowTestOutbound) PreMatchFlow(network string, destination netip.Addr) adapter.PreMatchAction {
	return adapter.PreMatchFlow
}

func (o *preMatchFlowTestOutbound) PortAddresses() (netip.Addr, netip.Addr) {
	return netip.Addr{}, netip.Addr{}
}

func (o *preMatchFlowTestOutbound) PortMTU() uint32 {
	return 0
}

func (o *preMatchFlowTestOutbound) AttachReturn(returnPath tun.Return) error {
	return nil
}

func (o *preMatchFlowTestOutbound) DetachReturn(returnPath tun.Return) error {
	return nil
}

func (o *preMatchFlowTestOutbound) WritePackets(packets [][]byte) error {
	return nil
}

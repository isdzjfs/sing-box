package route

import (
	"context"
	"io"
	"net"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/stretchr/testify/require"
)

func TestRouteDispatchesThroughResolvedGroup(t *testing.T) {
	for _, network := range []string{N.NetworkTCP, N.NetworkUDP} {
		t.Run(network, func(t *testing.T) {
			leaf := &routeDispatchTestOutbound{tag: network + "-leaf"}
			group := &routeDispatchTestGroup{tag: "auto", selected: leaf}
			connectionManager := &routeDispatchTestConnectionManager{}
			router := &Router{
				ctx:        context.Background(),
				logger:     log.NewNOPFactory().NewLogger("router"),
				outbound:   &routeDispatchTestOutboundManager{defaultOutbound: group},
				connection: connectionManager,
			}
			metadata := adapter.InboundContext{
				Destination: M.ParseSocksaddr("example.invalid:443"),
				Domain:      "example.invalid",
			}

			if network == N.NetworkTCP {
				localConn, remoteConn := net.Pipe()
				defer localConn.Close()
				defer remoteConn.Close()
				require.NoError(t, router.routeConnection(context.Background(), localConn, metadata, nil))
				require.True(t, group.connectionCalled)
				require.False(t, connectionManager.connectionCalled)
			} else {
				require.NoError(t, router.routePacketConnection(context.Background(), nil, metadata, nil))
				require.True(t, group.packetConnectionCalled)
				require.False(t, connectionManager.packetConnectionCalled)
			}
			require.Equal(t, []adapter.Outbound{group, leaf}, group.metadata.OutboundChain)
			require.False(t, group.attachCalled)
		})
	}
}

type routeDispatchTestOutbound struct {
	adapter.Outbound
	tag string
}

func (o *routeDispatchTestOutbound) Type() string      { return "test" }
func (o *routeDispatchTestOutbound) Tag() string       { return o.tag }
func (o *routeDispatchTestOutbound) Network() []string { return []string{N.NetworkTCP, N.NetworkUDP} }

type routeDispatchTestGroup struct {
	adapter.Outbound
	tag                    string
	selected               adapter.Outbound
	metadata               adapter.InboundContext
	connectionCalled       bool
	packetConnectionCalled bool
	attachCalled           bool
}

func (g *routeDispatchTestGroup) Type() string                     { return "test-group" }
func (g *routeDispatchTestGroup) Tag() string                      { return g.tag }
func (g *routeDispatchTestGroup) Network() []string                { return []string{N.NetworkTCP, N.NetworkUDP} }
func (g *routeDispatchTestGroup) All() []string                    { return []string{g.selected.Tag()} }
func (g *routeDispatchTestGroup) Selected(string) adapter.Outbound { return g.selected }
func (g *routeDispatchTestGroup) AttachConnection(io.Closer) func() {
	g.attachCalled = true
	return func() {}
}

func (g *routeDispatchTestGroup) NewConnection(_ context.Context, _ net.Conn, metadata adapter.InboundContext, _ N.CloseHandlerFunc) {
	g.connectionCalled = true
	g.metadata = metadata
}

func (g *routeDispatchTestGroup) NewPacketConnection(_ context.Context, _ N.PacketConn, metadata adapter.InboundContext, _ N.CloseHandlerFunc) {
	g.packetConnectionCalled = true
	g.metadata = metadata
}

type routeDispatchTestOutboundManager struct {
	adapter.OutboundManager
	defaultOutbound adapter.Outbound
}

func (m *routeDispatchTestOutboundManager) Default() adapter.Outbound { return m.defaultOutbound }

type routeDispatchTestConnectionManager struct {
	adapter.ConnectionManager
	connectionCalled       bool
	packetConnectionCalled bool
}

func (m *routeDispatchTestConnectionManager) NewConnection(context.Context, N.Dialer, net.Conn, adapter.InboundContext, N.CloseHandlerFunc) {
	m.connectionCalled = true
}

func (m *routeDispatchTestConnectionManager) NewPacketConnection(context.Context, N.Dialer, N.PacketConn, adapter.InboundContext, N.CloseHandlerFunc) {
	m.packetConnectionCalled = true
}

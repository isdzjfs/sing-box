package proxyprovider

import (
	"context"
	"net"
	"sync/atomic"

	"github.com/sagernet/sing-box/adapter"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
)

var (
	_ adapter.Outbound       = (*dynamicOutbound)(nil)
	_ adapter.OutboundServer = (*dynamicOutbound)(nil)
	_ adapter.Lifecycle      = (*dynamicOutbound)(nil)
)

type dynamicOutboundState struct {
	outbound adapter.Outbound
	server   M.Socksaddr
}

// dynamicOutbound keeps the identity used by groups and routing stable while a
// provider atomically replaces the concrete proxy implementation behind it.
type dynamicOutbound struct {
	tag   string
	state atomic.Pointer[dynamicOutboundState]
}

func newDynamicOutbound(tag string, outbound adapter.Outbound, server M.Socksaddr) *dynamicOutbound {
	dynamic := &dynamicOutbound{tag: tag}
	dynamic.state.Store(&dynamicOutboundState{outbound: outbound, server: server})
	return dynamic
}

func (d *dynamicOutbound) current() adapter.Outbound {
	state := d.state.Load()
	if state == nil {
		return nil
	}
	return state.outbound
}

func (d *dynamicOutbound) replace(outbound adapter.Outbound, server M.Socksaddr) adapter.Outbound {
	previous := d.state.Swap(&dynamicOutboundState{outbound: outbound, server: server})
	if previous == nil {
		return nil
	}
	return previous.outbound
}

func (d *dynamicOutbound) ServerAddress() M.Socksaddr {
	state := d.state.Load()
	if state == nil {
		return M.Socksaddr{}
	}
	return state.server
}

func (d *dynamicOutbound) Type() string {
	if outbound := d.current(); outbound != nil {
		return outbound.Type()
	}
	return "provider"
}

func (d *dynamicOutbound) Tag() string {
	return d.tag
}

func (d *dynamicOutbound) Network() []string {
	if outbound := d.current(); outbound != nil {
		return outbound.Network()
	}
	return nil
}

func (d *dynamicOutbound) Dependencies() []string {
	if outbound := d.current(); outbound != nil {
		return outbound.Dependencies()
	}
	return nil
}

func (d *dynamicOutbound) Start(stage adapter.StartStage) error {
	outbound := d.current()
	if outbound == nil {
		return E.New("proxy-provider outbound is unavailable: ", d.tag)
	}
	return adapter.LegacyStart(outbound, stage)
}

func (d *dynamicOutbound) Close() error {
	outbound := d.current()
	if outbound == nil {
		return nil
	}
	return closeProviderOutbound(outbound)
}

func (d *dynamicOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	outbound := d.current()
	if outbound == nil {
		return nil, E.New("proxy-provider outbound is unavailable: ", d.tag)
	}
	return outbound.DialContext(ctx, network, destination)
}

func (d *dynamicOutbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	outbound := d.current()
	if outbound == nil {
		return nil, E.New("proxy-provider outbound is unavailable: ", d.tag)
	}
	return outbound.ListenPacket(ctx, destination)
}

func closeProviderOutbound(outbound adapter.Outbound) error {
	if lifecycle, loaded := outbound.(adapter.Lifecycle); loaded {
		return lifecycle.Close()
	}
	if closer, loaded := outbound.(interface{ Close() error }); loaded {
		return closer.Close()
	}
	return nil
}

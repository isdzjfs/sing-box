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
	scope    *adapter.Scope
}

// dynamicOutbound keeps the identity used by groups and routing stable while a
// provider atomically replaces the concrete proxy implementation behind it.
type dynamicOutbound struct {
	tag   string
	state atomic.Pointer[dynamicOutboundState]
}

func newDynamicOutbound(tag string, target providerTarget) *dynamicOutbound {
	dynamic := &dynamicOutbound{tag: tag}
	dynamic.state.Store(&dynamicOutboundState{outbound: target.outbound, server: target.server, scope: target.scope})
	return dynamic
}

func (d *dynamicOutbound) current() adapter.Outbound {
	state := d.state.Load()
	if state == nil {
		return nil
	}
	return state.outbound
}

func (d *dynamicOutbound) replace(target providerTarget) *dynamicOutboundState {
	return d.state.Swap(&dynamicOutboundState{outbound: target.outbound, server: target.server, scope: target.scope})
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

func (d *dynamicOutbound) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	state := d.state.Load()
	if state == nil || state.outbound == nil {
		return E.New("proxy-provider outbound is unavailable: ", d.tag)
	}
	if stage == adapter.StartStateInitialize {
		// The wrapper stays stable; shutdown always closes its current target.
		scope.Add(d.Close)
	}
	if lifecycle, loaded := state.outbound.(adapter.Lifecycle); loaded {
		return lifecycle.Start(stage, state.scope)
	}
	return nil
}

func (d *dynamicOutbound) Close() error {
	state := d.state.Load()
	if state == nil {
		return nil
	}
	return state.scope.Close()
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

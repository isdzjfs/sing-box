package group

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/interrupt"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing/common/bufio"
	"github.com/stretchr/testify/require"
)

func TestSelectorInterruptsTrackedConnectionWhenSelectionChanges(t *testing.T) {
	localConn := &testURLTestConn{}
	staleOutbound := &testURLTestOutbound{tag: "stale"}
	freshOutbound := &testURLTestOutbound{tag: "fresh"}
	connectionManager := &testURLTestConnectionManager{}
	selector := newTestSelector(staleOutbound, freshOutbound, connectionManager, true)

	selector.NewConnection(context.Background(), localConn, adapter.InboundContext{}, nil)
	require.NotNil(t, connectionManager.conn)
	require.False(t, localConn.Closed())

	require.True(t, selector.SelectOutbound("fresh"))

	require.True(t, localConn.Closed())
}

func TestSelectorInterruptsTrackedPacketConnectionWhenSelectionChanges(t *testing.T) {
	localConn := &testURLTestPacketConn{}
	staleOutbound := &testURLTestOutbound{tag: "stale"}
	freshOutbound := &testURLTestOutbound{tag: "fresh"}
	connectionManager := &testURLTestConnectionManager{}
	selector := newTestSelector(staleOutbound, freshOutbound, connectionManager, true)

	selector.NewPacketConnection(context.Background(), bufio.NewPacketConn(localConn), adapter.InboundContext{}, nil)
	require.NotNil(t, connectionManager.packetConn)
	require.False(t, localConn.Closed())

	require.True(t, selector.SelectOutbound("fresh"))

	require.True(t, localConn.Closed())
}

func TestSelectorKeepsExternalConnectionWithoutInterruptOption(t *testing.T) {
	localConn := &testURLTestConn{}
	staleOutbound := &testURLTestOutbound{tag: "stale"}
	freshOutbound := &testURLTestOutbound{tag: "fresh"}
	connectionManager := &testURLTestConnectionManager{}
	selector := newTestSelector(staleOutbound, freshOutbound, connectionManager, false)

	selector.NewConnection(context.Background(), localConn, adapter.InboundContext{}, nil)
	require.NotNil(t, connectionManager.conn)

	require.True(t, selector.SelectOutbound("fresh"))

	require.False(t, localConn.Closed())
}

func newTestSelector(staleOutbound adapter.Outbound, freshOutbound adapter.Outbound, connectionManager adapter.ConnectionManager, interruptExternalConnections bool) *Selector {
	selector := &Selector{
		Adapter:                      outbound.NewAdapter(C.TypeSelector, "", nil, []string{staleOutbound.Tag(), freshOutbound.Tag()}),
		connection:                   connectionManager,
		outbounds:                    map[string]adapter.Outbound{staleOutbound.Tag(): staleOutbound, freshOutbound.Tag(): freshOutbound},
		interruptGroup:               interrupt.NewGroup(),
		interruptExternalConnections: interruptExternalConnections,
	}
	selector.selected.Store(staleOutbound)
	return selector
}

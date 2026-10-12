package proxyprovider

import (
	"context"
	"errors"
	"net"
	"os"
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/stretchr/testify/require"
)

type lifecycleTestOutbound struct {
	outbound.Adapter
	closed    atomic.Int32
	failStage *adapter.StartStage
}

func (o *lifecycleTestOutbound) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	if stage == adapter.StartStateInitialize {
		scope.Add(func() error { o.closed.Add(1); return nil })
	}
	if o.failStage != nil && stage == *o.failStage {
		return errors.New("replacement startup failure")
	}
	return nil
}

func (o *lifecycleTestOutbound) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return nil, os.ErrInvalid
}

func (o *lifecycleTestOutbound) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, os.ErrInvalid
}

func newLifecycleTestTarget() (providerTarget, *lifecycleTestOutbound) {
	m := &Manager{ctx: context.Background(), logger: log.NewNOPFactory().Logger()}
	node := &lifecycleTestOutbound{Adapter: outbound.NewAdapter("test", "node", nil, nil)}
	return providerTarget{outbound: node, scope: m.newTargetScope(node)}, node
}

func startLifecycleTestWrapper(t *testing.T, target providerTarget) (*dynamicOutbound, *adapter.Scope) {
	t.Helper()
	wrapper := newDynamicOutbound("node", target)
	scope := adapter.NewScope(context.Background(), log.NewNOPFactory().Logger())
	t.Cleanup(func() { require.NoError(t, scope.Close()) })
	for _, stage := range adapter.ListStartStages {
		require.NoError(t, wrapper.Start(stage, scope))
	}
	return wrapper, scope
}

func TestDynamicOutboundReplacementClosesEachTargetScope(t *testing.T) {
	first, firstNode := newLifecycleTestTarget()
	wrapper, scope := startLifecycleTestWrapper(t, first)
	second, secondNode := newLifecycleTestTarget()
	require.NoError(t, startProviderOutbound(second))
	previous := wrapper.replace(second)
	require.NoError(t, previous.scope.Close())
	require.ErrorIs(t, first.scope.Context().Err(), context.Canceled)
	require.EqualValues(t, 1, firstNode.closed.Load())
	require.Zero(t, secondNode.closed.Load())
	require.Same(t, secondNode, wrapper.current())
	require.NoError(t, scope.Close())
	require.EqualValues(t, 1, firstNode.closed.Load())
	require.EqualValues(t, 1, secondNode.closed.Load())
}

func TestDynamicOutboundFailedReplacementKeepsCurrentTarget(t *testing.T) {
	first, firstNode := newLifecycleTestTarget()
	wrapper, scope := startLifecycleTestWrapper(t, first)
	replacement, replacementNode := newLifecycleTestTarget()
	failStage := adapter.StartStatePostStart
	replacementNode.failStage = &failStage
	require.ErrorContains(t, startProviderOutbound(replacement), "replacement startup failure")
	closeProviderTargets(map[string]providerTarget{"node": replacement})
	require.EqualValues(t, 1, replacementNode.closed.Load())
	require.Zero(t, firstNode.closed.Load())
	require.Same(t, firstNode, wrapper.current())
	require.NoError(t, scope.Close())
	require.EqualValues(t, 1, firstNode.closed.Load())
	require.EqualValues(t, 1, replacementNode.closed.Load())
}

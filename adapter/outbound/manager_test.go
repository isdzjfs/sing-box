package outbound

import (
	"context"
	"errors"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/endpoint"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/stretchr/testify/require"
)

type scopedTestOutbound struct {
	Adapter
	stages    []adapter.StartStage
	closed    atomic.Int32
	failStage *adapter.StartStage
}

func (o *scopedTestOutbound) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	o.stages = append(o.stages, stage)
	if stage == adapter.StartStateInitialize {
		scope.Add(func() error { o.closed.Add(1); return nil })
	}
	if o.failStage != nil && stage == *o.failStage {
		return errors.New("test startup failure")
	}
	return nil
}

func (o *scopedTestOutbound) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return nil, os.ErrInvalid
}

func (o *scopedTestOutbound) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, os.ErrInvalid
}

func newScopedTestOutbound(tag string, dependencies ...string) *scopedTestOutbound {
	return &scopedTestOutbound{Adapter: NewAdapter("scoped-test", tag, nil, dependencies)}
}

func startScopedTestManager(t *testing.T) (*Manager, *adapter.Scope) {
	t.Helper()
	logger := log.NewNOPFactory().Logger()
	m := NewManager(logger, NewRegistry(), endpoint.NewManager(nil), "seed")
	require.NoError(t, m.Add(newScopedTestOutbound("seed")))
	scope := adapter.NewScope(context.Background(), logger)
	t.Cleanup(func() { require.NoError(t, scope.Close()) })
	for _, stage := range adapter.ListStartStages {
		require.NoError(t, scope.Start("outbound", m, stage))
	}
	return m, scope
}

func TestManagerRuntimeOutboundScopes(t *testing.T) {
	m, scope := startScopedTestManager(t)
	first := newScopedTestOutbound("provider-node")
	require.NoError(t, m.Add(first))
	require.Equal(t, adapter.ListStartStages, first.stages)
	require.NoError(t, m.Remove(first.Tag()))
	require.EqualValues(t, 1, first.closed.Load())
	second := newScopedTestOutbound(first.Tag())
	require.NoError(t, m.Add(second))
	require.Equal(t, adapter.ListStartStages, second.stages)
	require.NoError(t, scope.Close())
	require.EqualValues(t, 1, first.closed.Load())
	require.EqualValues(t, 1, second.closed.Load())
	require.Empty(t, m.scopes)
	require.ErrorIs(t, m.Add(newScopedTestOutbound("late")), os.ErrClosed)
}

func TestManagerFailedRuntimeStartupReleasesScope(t *testing.T) {
	m, _ := startScopedTestManager(t)
	stage := adapter.StartStatePostStart
	node := newScopedTestOutbound("broken")
	node.failStage = &stage
	require.ErrorContains(t, m.Add(node), "test startup failure")
	require.EqualValues(t, 1, node.closed.Load())
	_, loaded := m.Outbound(node.Tag())
	require.False(t, loaded)
	require.NotContains(t, m.scopes, node)
}

func TestManagerRejectedRemovalKeepsDependentOutbound(t *testing.T) {
	m, _ := startScopedTestManager(t)
	dependent := newScopedTestOutbound("group", "seed")
	require.NoError(t, m.Add(dependent))
	require.ErrorContains(t, m.Remove("seed"), "is depended by")
	seed, loaded := m.Outbound("seed")
	require.True(t, loaded)
	require.Zero(t, seed.(*scopedTestOutbound).closed.Load())
}

func TestManagerRuntimeCreateReplacesScopedOutbound(t *testing.T) {
	m, scope := startScopedTestManager(t)
	first := newScopedTestOutbound("replaceable")
	require.NoError(t, m.Add(first))
	second := newScopedTestOutbound(first.Tag())
	type testOptions struct{}
	Register(m.registry.(*Registry), "scoped-test", func(context.Context, adapter.Router, log.ContextLogger, string, testOptions) (adapter.Outbound, error) {
		return second, nil
	})
	require.NoError(t, m.Create(context.Background(), nil, m.logger, first.Tag(), "scoped-test", nil))
	require.Equal(t, adapter.ListStartStages, second.stages)
	require.EqualValues(t, 1, first.closed.Load())
	current, loaded := m.Outbound(first.Tag())
	require.True(t, loaded)
	require.Same(t, second, current)
	require.NoError(t, scope.Close())
	require.EqualValues(t, 1, second.closed.Load())
}

func TestManagerRuntimeAddRemoveDuringShutdown(t *testing.T) {
	m, scope := startScopedTestManager(t)
	nodes := make([]*scopedTestOutbound, 32)
	var workers sync.WaitGroup
	for i := range nodes {
		nodes[i] = newScopedTestOutbound(string(rune('a' + i)))
		node := nodes[i]
		workers.Go(func() {
			if m.Add(node) == nil {
				_ = m.Remove(node.Tag())
			}
		})
	}
	require.NoError(t, scope.Close())
	workers.Wait()
	for _, node := range nodes {
		if len(node.stages) > 0 {
			require.EqualValues(t, 1, node.closed.Load())
		}
	}
	require.Empty(t, m.scopes)
}

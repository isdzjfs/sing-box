package group

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/pause"
	"github.com/stretchr/testify/require"
)

func TestURLTestGroupPostStartStartsTicker(t *testing.T) {
	group := newTestURLTestGroup(t, 10*time.Millisecond, 100*time.Millisecond)
	group.PostStart()
	t.Cleanup(func() {
		require.NoError(t, group.Close())
	})

	require.Eventually(t, func() bool {
		group.access.Lock()
		defer group.access.Unlock()
		return group.ticker != nil
	}, time.Second, 10*time.Millisecond)
}

func TestURLTestGroupTickerStopsAfterIdleTimeout(t *testing.T) {
	group := newTestURLTestGroup(t, 10*time.Millisecond, 30*time.Millisecond)
	group.PostStart()

	require.Eventually(t, func() bool {
		group.access.Lock()
		defer group.access.Unlock()
		return group.ticker == nil
	}, time.Second, 10*time.Millisecond)
}

func TestURLTestCheckOutboundsRestartsTickerAfterIdleTimeout(t *testing.T) {
	group := newTestURLTestGroup(t, 10*time.Millisecond, 50*time.Millisecond)
	group.PostStart()
	require.Eventually(t, func() bool {
		group.access.Lock()
		defer group.access.Unlock()
		return group.ticker == nil
	}, time.Second, 10*time.Millisecond)

	outbound := &URLTest{group: group}
	outbound.CheckOutbounds()
	t.Cleanup(func() {
		require.NoError(t, group.Close())
	})

	require.Eventually(t, func() bool {
		group.access.Lock()
		defer group.access.Unlock()
		return group.ticker != nil
	}, time.Second, 10*time.Millisecond)
}

func TestURLTestGroupSkipsSharedOutboundRecentlyCheckedByAnotherGroup(t *testing.T) {
	ctx := context.Background()
	history := urltest.NewHistoryStorage()
	ctx = service.ContextWithPtr(ctx, history)
	ctx = pause.WithDefaultManager(ctx)

	outbound := &testURLTestOutbound{tag: "proxy"}
	outboundManager := &testURLTestOutboundManager{outbound: outbound}
	firstGroup, err := NewURLTestGroup(ctx, outboundManager, log.NewNOPFactory().Logger(), []adapter.Outbound{outbound}, "", time.Minute, 0, time.Minute, false)
	require.NoError(t, err)
	secondGroup, err := NewURLTestGroup(ctx, outboundManager, log.NewNOPFactory().Logger(), []adapter.Outbound{outbound}, "", time.Minute, 0, time.Minute, false)
	require.NoError(t, err)

	checkedAt := time.Unix(1000, 0)
	firstGroup.checkOutboundsAt(false, checkedAt)
	secondGroup.checkOutboundsAt(false, checkedAt)

	require.Equal(t, int32(1), outbound.dialCount.Load())
	histories := history.LoadURLTestHistories("proxy")
	require.Len(t, histories, 1)
	require.Equal(t, checkedAt, histories[0].Time)
	require.Equal(t, uint16(0), histories[0].Delay)
}

func newTestURLTestGroup(t *testing.T, interval time.Duration, idleTimeout time.Duration) *URLTestGroup {
	t.Helper()

	ctx := context.Background()
	history := urltest.NewHistoryStorage()
	ctx = service.ContextWithPtr(ctx, history)
	ctx = pause.WithDefaultManager(ctx)
	group, err := NewURLTestGroup(ctx, nil, log.NewNOPFactory().Logger(), nil, "", interval, 0, idleTimeout, false)
	require.NoError(t, err)
	return group
}

type testURLTestOutbound struct {
	tag       string
	dialCount atomic.Int32
}

func (o *testURLTestOutbound) Type() string {
	return "test"
}

func (o *testURLTestOutbound) Tag() string {
	return o.tag
}

func (o *testURLTestOutbound) Network() []string {
	return []string{N.NetworkTCP, N.NetworkUDP}
}

func (o *testURLTestOutbound) Dependencies() []string {
	return nil
}

func (o *testURLTestOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	o.dialCount.Add(1)
	return nil, errors.New("expected test dial failure")
}

func (o *testURLTestOutbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("not implemented")
}

type testURLTestOutboundManager struct {
	outbound adapter.Outbound
}

func (m *testURLTestOutboundManager) Start(stage adapter.StartStage) error {
	return nil
}

func (m *testURLTestOutboundManager) Close() error {
	return nil
}

func (m *testURLTestOutboundManager) Outbounds() []adapter.Outbound {
	return []adapter.Outbound{m.outbound}
}

func (m *testURLTestOutboundManager) Outbound(tag string) (adapter.Outbound, bool) {
	return m.outbound, tag == m.outbound.Tag()
}

func (m *testURLTestOutboundManager) Default() adapter.Outbound {
	return m.outbound
}

func (m *testURLTestOutboundManager) Remove(tag string) error {
	return errors.New("not implemented")
}

func (m *testURLTestOutboundManager) Create(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, outboundType string, options any) error {
	return errors.New("not implemented")
}

var _ adapter.OutboundManager = (*testURLTestOutboundManager)(nil)
var _ adapter.Outbound = (*testURLTestOutbound)(nil)

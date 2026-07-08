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

	secondGroup.checkOutboundsAt(false, checkedAt.Add(30*time.Second))

	require.Equal(t, int32(2), outbound.dialCount.Load())
	histories = history.LoadURLTestHistories("proxy")
	require.Len(t, histories, 2)
	require.Equal(t, checkedAt.Add(30*time.Second), histories[1].Time)
	require.Equal(t, uint16(0), histories[1].Delay)
}

func TestURLTestNowFallsBackToFirstSupportedOutboundBeforeSelection(t *testing.T) {
	ctx := newTestURLTestContext()
	firstOutbound := &testURLTestOutbound{tag: "first"}
	secondOutbound := &testURLTestOutbound{tag: "second"}
	group, err := NewURLTestGroup(ctx, nil, log.NewNOPFactory().Logger(), []adapter.Outbound{firstOutbound, secondOutbound}, "", time.Minute, 0, time.Minute, false)
	require.NoError(t, err)
	outbound := &URLTest{group: group}

	require.Equal(t, "first", outbound.Now())
	require.Nil(t, group.selectedOutboundTCP)
	require.Nil(t, group.selectedOutboundUDP)
}

func TestURLTestNowFallsBackToHistoryBestOutboundBeforeSelection(t *testing.T) {
	ctx := context.Background()
	history := urltest.NewHistoryStorage()
	ctx = service.ContextWithPtr(ctx, history)
	ctx = pause.WithDefaultManager(ctx)
	slowOutbound := &testURLTestOutbound{tag: "slow"}
	fastOutbound := &testURLTestOutbound{tag: "fast"}
	checkedAt := time.Unix(1000, 0)
	history.StoreURLTestHistory("slow", &adapter.URLTestHistory{
		Time:  checkedAt,
		Delay: 100,
	})
	history.StoreURLTestHistory("fast", &adapter.URLTestHistory{
		Time:  checkedAt,
		Delay: 10,
	})
	group, err := NewURLTestGroup(ctx, nil, log.NewNOPFactory().Logger(), []adapter.Outbound{slowOutbound, fastOutbound}, "", time.Minute, 0, time.Minute, false)
	require.NoError(t, err)
	outbound := &URLTest{group: group}

	require.Equal(t, "fast", outbound.Now())
	require.Nil(t, group.selectedOutboundTCP)
	require.Nil(t, group.selectedOutboundUDP)
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
	tag            string
	network        []string
	dialCount      atomic.Int32
	listenCount    atomic.Int32
	dialFn         func(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error)
	listenPacketFn func(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error)
}

func (o *testURLTestOutbound) Type() string {
	return "test"
}

func (o *testURLTestOutbound) Tag() string {
	return o.tag
}

func (o *testURLTestOutbound) Network() []string {
	if o.network != nil {
		return o.network
	}
	return []string{N.NetworkTCP, N.NetworkUDP}
}

func (o *testURLTestOutbound) Dependencies() []string {
	return nil
}

func (o *testURLTestOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	o.dialCount.Add(1)
	if o.dialFn != nil {
		return o.dialFn(ctx, network, destination)
	}
	return nil, errors.New("expected test dial failure")
}

func (o *testURLTestOutbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	o.listenCount.Add(1)
	if o.listenPacketFn != nil {
		return o.listenPacketFn(ctx, destination)
	}
	return nil, errors.New("not implemented")
}

func TestURLTestDiscardsConnectionDialedByStaleOutbound(t *testing.T) {
	ctx := newTestURLTestContext()
	staleConn := &testURLTestConn{}
	freshConn := &testURLTestConn{}
	staleOutbound := &testURLTestOutbound{tag: "stale"}
	freshOutbound := &testURLTestOutbound{tag: "fresh"}
	var group *URLTestGroup
	staleOutbound.dialFn = func(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
		group.selectedOutboundTCP = freshOutbound
		return staleConn, nil
	}
	freshOutbound.dialFn = func(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
		return freshConn, nil
	}
	var err error
	group, err = NewURLTestGroup(ctx, nil, log.NewNOPFactory().Logger(), []adapter.Outbound{staleOutbound, freshOutbound}, "", time.Minute, 0, time.Minute, true)
	require.NoError(t, err)
	group.selectedOutboundTCP = staleOutbound
	outbound := &URLTest{group: group, logger: log.NewNOPFactory().Logger()}

	conn, err := outbound.DialContext(context.Background(), N.NetworkTCP, M.ParseSocksaddr("example.com:443"))
	require.NoError(t, err)
	require.True(t, staleConn.Closed())
	require.False(t, freshConn.Closed())
	require.Equal(t, int32(1), staleOutbound.dialCount.Load())
	require.Equal(t, int32(1), freshOutbound.dialCount.Load())

	require.NoError(t, conn.Close())
	require.True(t, freshConn.Closed())
}

func TestURLTestDiscardsPacketConnectionListenedByStaleOutbound(t *testing.T) {
	ctx := newTestURLTestContext()
	staleConn := &testURLTestPacketConn{}
	freshConn := &testURLTestPacketConn{}
	staleOutbound := &testURLTestOutbound{tag: "stale"}
	freshOutbound := &testURLTestOutbound{tag: "fresh"}
	var group *URLTestGroup
	staleOutbound.listenPacketFn = func(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
		group.selectedOutboundUDP = freshOutbound
		return staleConn, nil
	}
	freshOutbound.listenPacketFn = func(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
		return freshConn, nil
	}
	var err error
	group, err = NewURLTestGroup(ctx, nil, log.NewNOPFactory().Logger(), []adapter.Outbound{staleOutbound, freshOutbound}, "", time.Minute, 0, time.Minute, true)
	require.NoError(t, err)
	group.selectedOutboundUDP = staleOutbound
	outbound := &URLTest{group: group, logger: log.NewNOPFactory().Logger()}

	conn, err := outbound.ListenPacket(context.Background(), M.ParseSocksaddr("example.com:443"))
	require.NoError(t, err)
	require.True(t, staleConn.Closed())
	require.False(t, freshConn.Closed())
	require.Equal(t, int32(1), staleOutbound.listenCount.Load())
	require.Equal(t, int32(1), freshOutbound.listenCount.Load())

	require.NoError(t, conn.Close())
	require.True(t, freshConn.Closed())
}

func newTestURLTestContext() context.Context {
	ctx := context.Background()
	ctx = service.ContextWithPtr(ctx, urltest.NewHistoryStorage())
	ctx = pause.WithDefaultManager(ctx)
	return ctx
}

type testURLTestConn struct {
	closed atomic.Bool
}

func (c *testURLTestConn) Read(p []byte) (n int, err error) {
	return 0, net.ErrClosed
}

func (c *testURLTestConn) Write(p []byte) (n int, err error) {
	return 0, net.ErrClosed
}

func (c *testURLTestConn) Close() error {
	c.closed.Store(true)
	return nil
}

func (c *testURLTestConn) LocalAddr() net.Addr {
	return &net.TCPAddr{}
}

func (c *testURLTestConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{}
}

func (c *testURLTestConn) SetDeadline(t time.Time) error {
	return nil
}

func (c *testURLTestConn) SetReadDeadline(t time.Time) error {
	return nil
}

func (c *testURLTestConn) SetWriteDeadline(t time.Time) error {
	return nil
}

func (c *testURLTestConn) Closed() bool {
	return c.closed.Load()
}

type testURLTestPacketConn struct {
	closed atomic.Bool
}

func (c *testURLTestPacketConn) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	return 0, nil, net.ErrClosed
}

func (c *testURLTestPacketConn) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	return 0, net.ErrClosed
}

func (c *testURLTestPacketConn) Close() error {
	c.closed.Store(true)
	return nil
}

func (c *testURLTestPacketConn) LocalAddr() net.Addr {
	return &net.UDPAddr{}
}

func (c *testURLTestPacketConn) SetDeadline(t time.Time) error {
	return nil
}

func (c *testURLTestPacketConn) SetReadDeadline(t time.Time) error {
	return nil
}

func (c *testURLTestPacketConn) SetWriteDeadline(t time.Time) error {
	return nil
}

func (c *testURLTestPacketConn) Closed() bool {
	return c.closed.Load()
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

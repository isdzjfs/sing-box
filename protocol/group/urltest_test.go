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
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/bufio"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/pause"
	"github.com/stretchr/testify/require"
)

func TestURLTestPreservesIcon(t *testing.T) {
	outbound, err := NewURLTest(context.Background(), nil, nil, "auto", option.URLTestOutboundOptions{
		Outbounds: []string{"proxy-a"},
		Icon:      "https://127.0.0.1:1/unavailable.png",
	})
	require.NoError(t, err)
	require.Equal(t, "https://127.0.0.1:1/unavailable.png", outbound.(adapter.OutboundGroupIcon).Icon())
}

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
		setTestSelectedTCP(group, freshOutbound)
		return staleConn, nil
	}
	freshOutbound.dialFn = func(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
		return freshConn, nil
	}
	var err error
	group, err = NewURLTestGroup(ctx, nil, log.NewNOPFactory().Logger(), []adapter.Outbound{staleOutbound, freshOutbound}, "", time.Minute, 0, time.Minute, true)
	require.NoError(t, err)
	setTestSelectedTCP(group, staleOutbound)
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
		setTestSelectedUDP(group, freshOutbound)
		return staleConn, nil
	}
	freshOutbound.listenPacketFn = func(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
		return freshConn, nil
	}
	var err error
	group, err = NewURLTestGroup(ctx, nil, log.NewNOPFactory().Logger(), []adapter.Outbound{staleOutbound, freshOutbound}, "", time.Minute, 0, time.Minute, true)
	require.NoError(t, err)
	setTestSelectedUDP(group, staleOutbound)
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

func TestURLTestRetriesConnectionWhenStaleOutboundDialFails(t *testing.T) {
	ctx := newTestURLTestContext()
	freshConn := &testURLTestConn{}
	staleOutbound := &testURLTestOutbound{tag: "stale"}
	freshOutbound := &testURLTestOutbound{tag: "fresh"}
	var group *URLTestGroup
	staleOutbound.dialFn = func(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
		setTestSelectedTCP(group, freshOutbound)
		return nil, errors.New("stale dial canceled")
	}
	freshOutbound.dialFn = func(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
		return freshConn, nil
	}
	var err error
	group, err = NewURLTestGroup(ctx, nil, log.NewNOPFactory().Logger(), []adapter.Outbound{staleOutbound, freshOutbound}, "", time.Minute, 0, time.Minute, true)
	require.NoError(t, err)
	setTestSelectedTCP(group, staleOutbound)
	group.history.StoreURLTestHistory("stale", &adapter.URLTestHistory{
		Time:  time.Unix(1000, 0),
		Delay: 10,
	})
	outbound := &URLTest{group: group, logger: log.NewNOPFactory().Logger()}

	conn, err := outbound.DialContext(context.Background(), N.NetworkTCP, M.ParseSocksaddr("example.com:443"))
	require.NoError(t, err)
	require.Equal(t, int32(1), staleOutbound.dialCount.Load())
	require.Equal(t, int32(1), freshOutbound.dialCount.Load())
	require.NotNil(t, group.history.LoadURLTestHistory("stale"))

	require.NoError(t, conn.Close())
	require.True(t, freshConn.Closed())
}

func TestURLTestRetriesPacketConnectionWhenStaleOutboundListenFails(t *testing.T) {
	ctx := newTestURLTestContext()
	freshConn := &testURLTestPacketConn{}
	staleOutbound := &testURLTestOutbound{tag: "stale"}
	freshOutbound := &testURLTestOutbound{tag: "fresh"}
	var group *URLTestGroup
	staleOutbound.listenPacketFn = func(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
		setTestSelectedUDP(group, freshOutbound)
		return nil, errors.New("stale packet dial canceled")
	}
	freshOutbound.listenPacketFn = func(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
		return freshConn, nil
	}
	var err error
	group, err = NewURLTestGroup(ctx, nil, log.NewNOPFactory().Logger(), []adapter.Outbound{staleOutbound, freshOutbound}, "", time.Minute, 0, time.Minute, true)
	require.NoError(t, err)
	setTestSelectedUDP(group, staleOutbound)
	group.history.StoreURLTestHistory("stale", &adapter.URLTestHistory{
		Time:  time.Unix(1000, 0),
		Delay: 10,
	})
	outbound := &URLTest{group: group, logger: log.NewNOPFactory().Logger()}

	conn, err := outbound.ListenPacket(context.Background(), M.ParseSocksaddr("example.com:443"))
	require.NoError(t, err)
	require.Equal(t, int32(1), staleOutbound.listenCount.Load())
	require.Equal(t, int32(1), freshOutbound.listenCount.Load())
	require.NotNil(t, group.history.LoadURLTestHistory("stale"))

	require.NoError(t, conn.Close())
	require.True(t, freshConn.Closed())
}

func TestURLTestDoesNotCancelPacketDialWhenTCPSelectionChanges(t *testing.T) {
	ctx, cancel := context.WithCancel(newTestURLTestContext())
	defer cancel()

	packetConn := &testURLTestPacketConn{}
	packetDialStarted := make(chan struct{}, 1)
	releasePacketDial := make(chan struct{})
	udpOutbound := &testURLTestOutbound{tag: "udp"}
	udpOutbound.listenPacketFn = func(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
		select {
		case packetDialStarted <- struct{}{}:
		default:
		}
		select {
		case <-releasePacketDial:
			return packetConn, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	tcpOutbound := &testURLTestOutbound{tag: "tcp"}
	tcpOutboundNext := &testURLTestOutbound{tag: "tcp-next"}
	tcpOutboundFinal := &testURLTestOutbound{tag: "tcp-final"}

	group, err := NewURLTestGroup(ctx, nil, log.NewNOPFactory().Logger(), nil, "", time.Minute, 0, time.Minute, true)
	require.NoError(t, err)
	setTestSelectedTCP(group, tcpOutbound)
	setTestSelectedUDP(group, udpOutbound)
	outbound := &URLTest{group: group, logger: log.NewNOPFactory().Logger()}

	type listenResult struct {
		conn net.PacketConn
		err  error
	}
	resultCh := make(chan listenResult, 1)
	go func() {
		conn, err := outbound.ListenPacket(ctx, M.ParseSocksaddr("example.com:443"))
		resultCh <- listenResult{conn: conn, err: err}
	}()

	require.Eventually(t, func() bool {
		select {
		case <-packetDialStarted:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)

	updated, generation := group.applySelectedUpdate(tcpOutboundNext, true, nil, false)
	require.True(t, updated)
	group.interruptGroup.InterruptBefore(generation, true)
	updated, generation = group.applySelectedUpdate(tcpOutboundFinal, true, nil, false)
	require.True(t, updated)
	group.interruptGroup.InterruptBefore(generation, true)

	close(releasePacketDial)
	var result listenResult
	require.Eventually(t, func() bool {
		select {
		case result = <-resultCh:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
	require.NoError(t, result.err)
	require.Equal(t, int32(1), udpOutbound.listenCount.Load())
	require.NoError(t, result.conn.Close())
}

func TestURLTestCancelsStaleDialWhenSelectionChanges(t *testing.T) {
	ctx := newTestURLTestContext()
	freshConn := &testURLTestConn{}
	staleOutbound := &testURLTestOutbound{tag: "stale"}
	freshOutbound := &testURLTestOutbound{tag: "fresh"}
	staleDialStarted := make(chan struct{})
	staleOutbound.dialFn = func(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
		close(staleDialStarted)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	freshOutbound.dialFn = func(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
		return freshConn, nil
	}
	group, err := NewURLTestGroup(ctx, nil, log.NewNOPFactory().Logger(), []adapter.Outbound{staleOutbound, freshOutbound}, "", time.Minute, 0, time.Minute, true)
	require.NoError(t, err)
	setTestSelectedTCP(group, staleOutbound)
	outbound := &URLTest{group: group, logger: log.NewNOPFactory().Logger()}

	type dialResult struct {
		conn net.Conn
		err  error
	}
	resultCh := make(chan dialResult, 1)
	go func() {
		conn, err := outbound.DialContext(context.Background(), N.NetworkTCP, M.ParseSocksaddr("example.com:443"))
		resultCh <- dialResult{conn: conn, err: err}
	}()

	require.Eventually(t, func() bool {
		select {
		case <-staleDialStarted:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
	updated, generation := group.applySelectedUpdate(freshOutbound, true, nil, false)
	require.True(t, updated)
	group.interruptGroup.InterruptBefore(generation, true)

	var result dialResult
	require.Eventually(t, func() bool {
		select {
		case result = <-resultCh:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
	require.NoError(t, result.err)
	require.Equal(t, int32(1), staleOutbound.dialCount.Load())
	require.Equal(t, int32(1), freshOutbound.dialCount.Load())

	require.NoError(t, result.conn.Close())
	require.True(t, freshConn.Closed())
}

func TestURLTestInterruptSkipsNewGenerationConnections(t *testing.T) {
	ctx := newTestURLTestContext()
	oldConn := &testURLTestConn{}
	newConn := &testURLTestConn{}
	staleOutbound := &testURLTestOutbound{tag: "stale"}
	freshOutbound := &testURLTestOutbound{tag: "fresh"}
	group, err := NewURLTestGroup(ctx, nil, log.NewNOPFactory().Logger(), []adapter.Outbound{staleOutbound, freshOutbound}, "", time.Minute, 0, time.Minute, true)
	require.NoError(t, err)
	setTestSelectedTCP(group, staleOutbound)

	_ = group.interruptGroup.NewConnWithGeneration(oldConn, true, 0)
	updated, generation := group.applySelectedUpdate(freshOutbound, true, nil, false)
	require.True(t, updated)
	trackedNewConn := group.interruptGroup.NewConnWithGeneration(newConn, true, generation)

	group.interruptGroup.InterruptBefore(generation, true)

	require.True(t, oldConn.Closed())
	require.False(t, newConn.Closed())
	require.NoError(t, trackedNewConn.Close())
	require.True(t, newConn.Closed())
}

func TestURLTestInterruptsFallbackConnectionWhenFirstCheckSelectsDifferentOutbound(t *testing.T) {
	ctx := newTestURLTestContext()
	staleConn := &testURLTestConn{}
	staleOutbound := &testURLTestOutbound{tag: "stale"}
	freshOutbound := &testURLTestOutbound{tag: "fresh"}
	staleOutbound.dialFn = func(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
		return staleConn, nil
	}
	group, err := NewURLTestGroup(ctx, nil, log.NewNOPFactory().Logger(), []adapter.Outbound{staleOutbound, freshOutbound}, "", time.Minute, 0, time.Minute, true)
	require.NoError(t, err)
	outbound := &URLTest{group: group, logger: log.NewNOPFactory().Logger()}

	_, err = outbound.DialContext(context.Background(), N.NetworkTCP, M.ParseSocksaddr("example.com:443"))
	require.NoError(t, err)
	require.False(t, staleConn.Closed())
	require.Nil(t, group.selectedOutboundTCP)

	group.history.StoreURLTestHistory("fresh", &adapter.URLTestHistory{
		Time:  time.Unix(1000, 0),
		Delay: 10,
	})
	group.performUpdateCheck()

	require.True(t, staleConn.Closed())
	require.Equal(t, freshOutbound, group.selectedOutboundTCP)
}

func TestURLTestInterruptsFallbackPacketConnectionWhenFirstCheckSelectsDifferentOutbound(t *testing.T) {
	ctx := newTestURLTestContext()
	staleConn := &testURLTestPacketConn{}
	staleOutbound := &testURLTestOutbound{tag: "stale"}
	freshOutbound := &testURLTestOutbound{tag: "fresh"}
	staleOutbound.listenPacketFn = func(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
		return staleConn, nil
	}
	group, err := NewURLTestGroup(ctx, nil, log.NewNOPFactory().Logger(), []adapter.Outbound{staleOutbound, freshOutbound}, "", time.Minute, 0, time.Minute, true)
	require.NoError(t, err)
	outbound := &URLTest{group: group, logger: log.NewNOPFactory().Logger()}

	_, err = outbound.ListenPacket(context.Background(), M.ParseSocksaddr("example.com:443"))
	require.NoError(t, err)
	require.False(t, staleConn.Closed())
	require.Nil(t, group.selectedOutboundUDP)

	group.history.StoreURLTestHistory("fresh", &adapter.URLTestHistory{
		Time:  time.Unix(1000, 0),
		Delay: 10,
	})
	group.performUpdateCheck()

	require.True(t, staleConn.Closed())
	require.Equal(t, freshOutbound, group.selectedOutboundUDP)
}

func TestURLTestInterruptsTrackedConnectionAfterDialGenerationCommitted(t *testing.T) {
	ctx := newTestURLTestContext()
	localConn := &testURLTestConn{}
	remoteConn := &testURLTestConn{}
	staleOutbound := &testURLTestOutbound{tag: "stale"}
	freshOutbound := &testURLTestOutbound{tag: "fresh"}
	freshOutbound.dialFn = func(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
		return remoteConn, nil
	}
	group, err := NewURLTestGroup(ctx, nil, log.NewNOPFactory().Logger(), []adapter.Outbound{staleOutbound, freshOutbound}, "", time.Minute, 0, time.Minute, true)
	require.NoError(t, err)
	connectionManager := &testURLTestConnectionManager{}
	outbound := &URLTest{group: group, connection: connectionManager}

	outbound.NewConnection(context.Background(), localConn, adapter.InboundContext{}, nil)
	require.NotNil(t, connectionManager.conn)
	require.False(t, localConn.Closed())

	group.history.StoreURLTestHistory("fresh", &adapter.URLTestHistory{
		Time:  time.Unix(1000, 0),
		Delay: 10,
	})
	group.performUpdateCheck()

	require.False(t, localConn.Closed())
	require.Equal(t, freshOutbound, group.selectedOutboundTCP)

	_, err = connectionManager.this.DialContext(connectionManager.ctx, N.NetworkTCP, M.ParseSocksaddr("example.com:443"))
	require.NoError(t, err)
	require.False(t, localConn.Closed())
	require.False(t, remoteConn.Closed())

	updated, generation := group.applySelectedUpdate(staleOutbound, true, nil, false)
	require.True(t, updated)
	group.interruptGroup.InterruptBefore(generation, true)

	require.True(t, localConn.Closed())
	require.True(t, remoteConn.Closed())
}

func TestURLTestInterruptsTrackedPacketConnectionAfterListenGenerationCommitted(t *testing.T) {
	ctx := newTestURLTestContext()
	localConn := &testURLTestPacketConn{}
	remoteConn := &testURLTestPacketConn{}
	staleOutbound := &testURLTestOutbound{tag: "stale"}
	freshOutbound := &testURLTestOutbound{tag: "fresh"}
	freshOutbound.listenPacketFn = func(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
		return remoteConn, nil
	}
	group, err := NewURLTestGroup(ctx, nil, log.NewNOPFactory().Logger(), []adapter.Outbound{staleOutbound, freshOutbound}, "", time.Minute, 0, time.Minute, true)
	require.NoError(t, err)
	connectionManager := &testURLTestConnectionManager{}
	outbound := &URLTest{group: group, connection: connectionManager}

	outbound.NewPacketConnection(context.Background(), bufio.NewPacketConn(localConn), adapter.InboundContext{}, nil)
	require.NotNil(t, connectionManager.packetConn)
	require.False(t, localConn.Closed())

	group.history.StoreURLTestHistory("fresh", &adapter.URLTestHistory{
		Time:  time.Unix(1000, 0),
		Delay: 10,
	})
	group.performUpdateCheck()

	require.False(t, localConn.Closed())
	require.Equal(t, freshOutbound, group.selectedOutboundUDP)

	_, err = connectionManager.this.ListenPacket(connectionManager.ctx, M.ParseSocksaddr("example.com:443"))
	require.NoError(t, err)
	require.False(t, localConn.Closed())
	require.False(t, remoteConn.Closed())

	updated, generation := group.applySelectedUpdate(nil, false, staleOutbound, true)
	require.True(t, updated)
	group.interruptGroup.InterruptBefore(generation, true)

	require.True(t, localConn.Closed())
	require.True(t, remoteConn.Closed())
}

func newTestURLTestContext() context.Context {
	ctx := context.Background()
	ctx = service.ContextWithPtr(ctx, urltest.NewHistoryStorage())
	ctx = pause.WithDefaultManager(ctx)
	return ctx
}

func setTestSelectedTCP(group *URLTestGroup, outbound adapter.Outbound) {
	group.selectedAccess.Lock()
	defer group.selectedAccess.Unlock()
	group.selectedOutboundTCP = outbound
}

func setTestSelectedUDP(group *URLTestGroup, outbound adapter.Outbound) {
	group.selectedAccess.Lock()
	defer group.selectedAccess.Unlock()
	group.selectedOutboundUDP = outbound
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

type testURLTestConnectionManager struct {
	ctx        context.Context
	this       N.Dialer
	conn       net.Conn
	packetConn N.PacketConn
}

func (m *testURLTestConnectionManager) Start(stage adapter.StartStage) error {
	return nil
}

func (m *testURLTestConnectionManager) Close() error {
	return nil
}

func (m *testURLTestConnectionManager) Count() int {
	return 0
}

func (m *testURLTestConnectionManager) CloseAll() {
}

func (m *testURLTestConnectionManager) TrackConn(conn net.Conn) net.Conn {
	return conn
}

func (m *testURLTestConnectionManager) TrackPacketConn(conn net.PacketConn) net.PacketConn {
	return conn
}

func (m *testURLTestConnectionManager) NewConnection(ctx context.Context, this N.Dialer, conn net.Conn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	m.ctx = ctx
	m.this = this
	m.conn = conn
}

func (m *testURLTestConnectionManager) NewPacketConnection(ctx context.Context, this N.Dialer, conn N.PacketConn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	m.ctx = ctx
	m.this = this
	m.packetConn = conn
}

func (m *testURLTestOutboundManager) Remove(tag string) error {
	return errors.New("not implemented")
}

func (m *testURLTestOutboundManager) Create(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, outboundType string, options any) error {
	return errors.New("not implemented")
}

var _ adapter.OutboundManager = (*testURLTestOutboundManager)(nil)
var _ adapter.Outbound = (*testURLTestOutbound)(nil)

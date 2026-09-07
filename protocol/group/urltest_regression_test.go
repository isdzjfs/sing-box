package group

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
)

type blockedNetworkOutbound struct {
	*testURLTestOutbound
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (o *blockedNetworkOutbound) Network() []string {
	o.once.Do(func() {
		close(o.entered)
		<-o.release
	})
	return o.testURLTestOutbound.Network()
}

func TestURLTestRefreshCannotRecommitRemovedMember(t *testing.T) {
	ctx := newTestURLTestContext()
	history := service.PtrFromContext[urltest.HistoryStorage](ctx)
	old := &blockedNetworkOutbound{
		testURLTestOutbound: &testURLTestOutbound{tag: "removed"},
		entered:             make(chan struct{}), release: make(chan struct{}),
	}
	fresh := &testURLTestOutbound{tag: "current"}
	manager := &testURLTestOutboundManager{outbounds: map[string]adapter.Outbound{"removed": old, "current": fresh}}
	g, err := NewURLTestGroup(ctx, manager, log.NewNOPFactory().Logger(), []adapter.Outbound{old}, "", time.Minute, 50, time.Minute, false)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	// Suppress probe I/O; retain the real concurrent selection/update code.
	g.checking.Store(true)
	history.StoreURLTestHistory("removed", &adapter.URLTestHistory{Time: time.Now(), Delay: 10})
	history.StoreURLTestHistory("current", &adapter.URLTestHistory{Time: time.Now(), Delay: 100})
	done := make(chan struct{})
	go func() { g.performUpdateCheck(); close(done) }()
	select {
	case <-old.entered:
	case <-time.After(time.Second):
		t.Fatal("selection did not reach old member")
	}
	g.UpdateOutbounds([]adapter.Outbound{fresh})
	close(old.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("selection did not finish")
	}
	if selected := g.selectedOutbound(N.NetworkTCP).outbound; selected != fresh {
		t.Fatalf("removed outbound recommitted after refresh: members=[current], selected=%s", selected.Tag())
	}
}

func TestURLTestDistinctProbeURLsMustNotReuseOtherTargetSuccess(t *testing.T) {
	ctx := newTestURLTestContext()
	history := service.PtrFromContext[urltest.HistoryStorage](ctx)
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Millisecond)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer fixture.Close()
	node := &testURLTestOutbound{tag: "shared", dialFn: func(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
		if destination.Fqdn == "blocked.invalid" {
			return nil, errors.New("second target unavailable")
		}
		return (&net.Dialer{}).DialContext(ctx, network, fixture.Listener.Addr().String())
	}}
	manager := &testURLTestOutboundManager{outbound: node}
	first, err := NewURLTestGroup(ctx, manager, log.NewNOPFactory().Logger(), []adapter.Outbound{node}, "http://first.invalid/probe", time.Minute, 50, time.Minute, false)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := NewURLTestGroup(ctx, manager, log.NewNOPFactory().Logger(), []adapter.Outbound{node}, "http://blocked.invalid/probe", time.Minute, 50, time.Minute, false)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	checkedAt := time.Now()
	first.checkOutboundsAt(false, checkedAt)
	if history.LoadURLTestHistory("shared") == nil {
		t.Fatal("first fixture must succeed")
	}
	second.checkOutboundsAt(false, checkedAt)
	if node.dialCount.Load() != 2 {
		_, healthy := second.Select(N.NetworkTCP)
		t.Fatalf("different target was not probed: dials=%d, blocked-target group treats shared node as tested=%v", node.dialCount.Load(), healthy)
	}
}

func TestURLTestToleranceAdditionMustNotWrap(t *testing.T) {
	ctx := newTestURLTestContext()
	history := service.PtrFromContext[urltest.HistoryStorage](ctx)
	fast := &testURLTestOutbound{tag: "fast"}
	slow := &testURLTestOutbound{tag: "slow"}
	manager := &testURLTestOutboundManager{outbounds: map[string]adapter.Outbound{"fast": fast, "slow": slow}}
	g, err := NewURLTestGroup(ctx, manager, log.NewNOPFactory().Logger(), []adapter.Outbound{fast, slow}, "", time.Minute, 65400, time.Minute, false)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	history.StoreURLTestHistory("fast", &adapter.URLTestHistory{Time: time.Now(), Delay: 100})
	history.StoreURLTestHistory("slow", &adapter.URLTestHistory{Time: time.Now(), Delay: 200})
	setTestSelectedTCP(g, fast)
	if selected, _ := g.Select(N.NetworkTCP); selected != fast {
		t.Fatalf("tolerance overflow picked slower node: selected=%s, fast=100ms, slow=200ms, tolerance=65400ms", selected.Tag())
	}
}

func TestURLTestNegativeIntervalMustBeRejected(t *testing.T) {
	g, err := NewURLTestGroup(newTestURLTestContext(), nil, log.NewNOPFactory().Logger(), nil, "", -time.Second, 50, time.Minute, false)
	if err != nil {
		return
	}
	defer g.Close()
	defer func() {
		if problem := recover(); problem != nil {
			t.Errorf("negative interval accepted and crashed PostStart: %v", problem)
		}
	}()
	g.PostStart()
	t.Error("negative interval was accepted")
}

func TestURLTestNowMustDescribeColdStartDialFallback(t *testing.T) {
	ctx := newTestURLTestContext()
	node := &testURLTestOutbound{tag: "fallback", dialFn: func(context.Context, string, M.Socksaddr) (net.Conn, error) { return &testURLTestConn{}, nil }}
	manager := &testURLTestOutboundManager{outbound: node}
	g, err := NewURLTestGroup(ctx, manager, log.NewNOPFactory().Logger(), []adapter.Outbound{node}, "", time.Minute, 50, time.Minute, false)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	outbound := &URLTest{group: g, outbound: manager}
	conn, err := outbound.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr("example.invalid:443"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if got := outbound.Now(); got != node.Tag() {
		t.Fatalf("dial used fallback successfully, but Now() reports %q", got)
	}
	if g.selectedOutbound(N.NetworkTCP).outbound != nil || g.tcpGeneration != 0 {
		t.Fatal("display fallback must not commit the initial selection")
	}
}

func TestURLTestNestedTCPOnlyGroupMustNotWinUDPSelection(t *testing.T) {
	ctx := newTestURLTestContext()
	history := service.PtrFromContext[urltest.HistoryStorage](ctx)
	tcpOnly := &testURLTestOutbound{tag: "tcp-only", network: []string{N.NetworkTCP}}
	udp := &testURLTestOutbound{tag: "udp-capable", listenPacketFn: func(context.Context, M.Socksaddr) (net.PacketConn, error) {
		return &testURLTestPacketConn{}, nil
	}}
	manager := &testURLTestOutboundManager{outbounds: map[string]adapter.Outbound{"tcp-only": tcpOnly, "udp-capable": udp}}
	ctx = service.ContextWith[adapter.OutboundManager](ctx, manager)
	create := func(tag string, members []string) *URLTest {
		item, err := NewURLTest(ctx, nil, log.NewNOPFactory().Logger(), tag, option.URLTestOutboundOptions{Outbounds: members})
		if err != nil {
			t.Fatal(err)
		}
		g := item.(*URLTest)
		manager.outbounds[tag] = g
		if err := g.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { g.Close() })
		return g
	}
	inner := create("inner", []string{"tcp-only"})
	outer := create("outer", []string{"inner", "udp-capable"})
	history.StoreURLTestHistory("tcp-only", &adapter.URLTestHistory{Time: time.Now(), Delay: 10})
	history.StoreURLTestHistory("udp-capable", &adapter.URLTestHistory{Time: time.Now(), Delay: 100})
	inner.PerformUpdateCheck()
	outer.PerformUpdateCheck()
	conn, err := outer.ListenPacket(ctx, M.ParseSocksaddr("example.invalid:443"))
	if err != nil {
		t.Fatalf("healthy UDP alternative ignored: outer UDP selection=%s, error=%v, alternate UDP dials=%d", outer.group.selectedOutbound(N.NetworkUDP).outbound.Tag(), err, udp.listenCount.Load())
	}
	conn.Close()
	if got := outer.NowForNetwork(N.NetworkUDP); got != "udp-capable" {
		t.Fatalf("UDP chain reports wrong selected member: %s", got)
	}
	// Start using the inner group for UDP, then remove its only UDP member.
	inner.group.checking.Store(true)
	inner.group.UpdateOutbounds([]adapter.Outbound{udp})
	outer.group.applySelectedUpdate(nil, false, inner, true)
	inner.group.UpdateOutbounds([]adapter.Outbound{tcpOnly})
	conn, err = outer.ListenPacket(ctx, M.ParseSocksaddr("example.invalid:443"))
	if err != nil {
		t.Fatalf("nested member refresh must move UDP to the healthy alternative: %v", err)
	}
	conn.Close()
	if got := outer.NowForNetwork(N.NetworkUDP); got != "udp-capable" {
		t.Fatalf("stale UDP selection survived nested refresh: %s", got)
	}
}

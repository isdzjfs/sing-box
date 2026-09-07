package daemon

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/protocol/group"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/pause"
)

type urlTestManager struct {
	adapter.OutboundManager
	items map[string]adapter.Outbound
}

func (m *urlTestManager) Outbound(tag string) (adapter.Outbound, bool) {
	item, ok := m.items[tag]
	return item, ok
}
func (m *urlTestManager) Outbounds() []adapter.Outbound {
	var result []adapter.Outbound
	for _, item := range m.items {
		result = append(result, item)
	}
	return result
}

type urlTestFailingNode struct{ outbound.Adapter }

func (n *urlTestFailingNode) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return nil, errors.New("node unavailable")
}
func (n *urlTestFailingNode) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("node unavailable")
}

func newURLTestService(t *testing.T) (*StartedService, *group.URLTest, *urltest.HistoryStorage) {
	t.Helper()
	ctx := service.ContextWithDefaultRegistry(context.Background())
	history := urltest.NewHistoryStorage()
	ctx = service.ContextWithPtr(ctx, history)
	ctx = pause.WithDefaultManager(ctx)
	manager := &urlTestManager{items: map[string]adapter.Outbound{}}
	ctx = service.ContextWith[adapter.OutboundManager](ctx, manager)
	for _, tag := range []string{"a", "b"} {
		manager.items[tag] = &urlTestFailingNode{Adapter: outbound.NewAdapter("audit", tag, []string{N.NetworkTCP, N.NetworkUDP}, nil)}
	}
	auto, err := group.NewURLTest(ctx, nil, log.NewNOPFactory().Logger(), "auto", option.URLTestOutboundOptions{Outbounds: []string{"a", "b"}})
	if err != nil {
		t.Fatal(err)
	}
	g := auto.(*group.URLTest)
	manager.items["auto"] = g
	if err := g.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Close() })
	history.StoreURLTestHistory("a", &adapter.URLTestHistory{Time: time.Now().Add(-time.Second), Delay: 100})
	history.StoreURLTestHistory("b", &adapter.URLTestHistory{Time: time.Now().Add(-time.Second), Delay: 200})
	g.PerformUpdateCheck()
	if g.Now() != "a" {
		t.Fatal("fixture must initially select a")
	}
	s := &StartedService{ctx: ctx, instance: &Instance{ctx: ctx, outboundManager: manager, urlTestHistoryStorage: history}, serviceStatus: &ServiceStatus{Status: ServiceStatus_STARTED}}
	return s, g, history
}

func TestManualURLTestV2FailurePreservesSelection(t *testing.T) {
	s, g, history := newURLTestService(t)
	result, err := s.URLTestItemsV2(context.Background(), &URLTestItemsV2Request{RequestId: "audit", OutboundTag: "auto", ItemTags: []string{"a"}, TimeoutMillis: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Results) != 1 || result.Results[0].ErrorMessage == "" || result.Results[0].Sequence == 0 {
		t.Fatal("fixture must return a failed probe")
	}
	if history.LoadURLTestHistory("a") != nil {
		t.Fatal("failed probe must invalidate a")
	}
	if got := g.Now(); got != "a" {
		t.Fatalf("single-node test must preserve the automatic selection, got=%s", got)
	}
}

func TestGroupSnapshotExposesLatestFailure(t *testing.T) {
	s, _, history := newURLTestService(t)
	checkedAt := time.Now()
	failed := history.StoreURLTestFailure("a", checkedAt)
	groups := s.readGroups()
	for _, g := range groups.Group {
		for _, item := range g.Items {
			if item.Tag == "a" && (item.UrlTestTimeMillis != checkedAt.UnixMilli() || item.UrlTestSequence != failed.Sequence || item.UrlTestDelay != 0) {
				t.Fatalf("latest failure indistinguishable from never tested: delay=%d time=%d", item.UrlTestDelay, item.UrlTestTime)
			}
		}
	}
}

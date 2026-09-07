package clashmode

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/common/observable"
	"github.com/sagernet/sing/service"
)

func TestSetModeClosesConnectionsAndNotifies(t *testing.T) {
	ctx := service.ContextWithDefaultRegistry(context.Background())
	dnsRouter := new(testModeSwitchDNSRouter)
	service.MustRegister[adapter.DNSRouter](ctx, dnsRouter)
	manager := NewManager(ctx, log.NewNOPFactory().Logger(), "Rule", []string{"Rule", "Global"})
	trafficManager := new(testModeSwitchTrafficManager)
	network := new(testModeSwitchNetwork)
	manager.trafficManager = trafficManager
	manager.network = network
	subscriber := observable.NewSubscriber[struct{}](1)
	defer subscriber.Close()
	manager.AddUpdateHook(subscriber)
	updates, _ := subscriber.Subscription()

	manager.SetMode("global")
	if manager.Mode() != "Global" || trafficManager.closeAllConnectionsCount != 1 || network.resetNetworkCount != 1 || dnsRouter.clearCount != 1 {
		t.Fatal("mode change did not reset existing connections and DNS")
	}
	select {
	case <-updates:
	default:
		t.Fatal("mode change did not notify reference and UI subscribers")
	}

	manager.SetMode("Global")
	manager.SetMode("invalid")
	if trafficManager.closeAllConnectionsCount != 1 || network.resetNetworkCount != 1 || dnsRouter.clearCount != 1 {
		t.Fatal("unchanged or invalid mode reset connections")
	}
}

func TestSetModeWithoutTrafficManager(t *testing.T) {
	ctx := service.ContextWithDefaultRegistry(context.Background())
	service.MustRegister[adapter.DNSRouter](ctx, new(testModeSwitchDNSRouter))
	manager := NewManager(ctx, log.NewNOPFactory().Logger(), "Rule", []string{"Global"})
	manager.SetMode("Global")
}

type testModeSwitchDNSRouter struct {
	adapter.DNSRouter
	clearCount int
}

func (r *testModeSwitchDNSRouter) ClearCache() {
	r.clearCount++
}

func TestCloseConnectionsForModeSwitch(t *testing.T) {
	trafficManager := new(testModeSwitchTrafficManager)
	network := new(testModeSwitchNetwork)

	closeConnectionsForModeSwitch(context.Background(), trafficManager, network)

	if trafficManager.closeAllConnectionsCount != 1 {
		t.Fatalf("expected CloseAllConnections to be called once, got %d", trafficManager.closeAllConnectionsCount)
	}
	if network.resetNetworkCount != 1 {
		t.Fatalf("expected ResetNetwork to be called once, got %d", network.resetNetworkCount)
	}
}

func TestCloseConnectionsForModeSwitchAllowsMissingNetwork(t *testing.T) {
	trafficManager := new(testModeSwitchTrafficManager)

	closeConnectionsForModeSwitch(context.Background(), trafficManager, nil)

	if trafficManager.closeAllConnectionsCount != 1 {
		t.Fatalf("expected CloseAllConnections to be called once, got %d", trafficManager.closeAllConnectionsCount)
	}
}

type testModeSwitchTrafficManager struct {
	closeAllConnectionsCount int
}

func (m *testModeSwitchTrafficManager) CloseAllConnections() {
	m.closeAllConnectionsCount++
}

type testModeSwitchNetwork struct {
	resetNetworkCount int
}

func (n *testModeSwitchNetwork) ResetNetwork(context.Context) {
	n.resetNetworkCount++
}

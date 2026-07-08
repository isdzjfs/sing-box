package clashapi

import "testing"

func TestCloseConnectionsForModeSwitch(t *testing.T) {
	trafficManager := new(testModeSwitchTrafficManager)
	network := new(testModeSwitchNetwork)

	closeConnectionsForModeSwitch(trafficManager, network)

	if trafficManager.closeAllConnectionsCount != 1 {
		t.Fatalf("expected CloseAllConnections to be called once, got %d", trafficManager.closeAllConnectionsCount)
	}
	if network.resetNetworkCount != 1 {
		t.Fatalf("expected ResetNetwork to be called once, got %d", network.resetNetworkCount)
	}
}

func TestCloseConnectionsForModeSwitchAllowsMissingNetwork(t *testing.T) {
	trafficManager := new(testModeSwitchTrafficManager)

	closeConnectionsForModeSwitch(trafficManager, nil)

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

func (n *testModeSwitchNetwork) ResetNetwork() {
	n.resetNetworkCount++
}

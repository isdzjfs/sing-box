package daemon

import (
	"context"
	"testing"

	"github.com/sagernet/sing/service"
)

func TestReloadPreparationFailureKeepsCurrentInstance(t *testing.T) {
	startedService := NewStartedService(ServiceOptions{Context: service.ContextWithDefaultRegistry(context.Background())})
	currentInstance := &Instance{}
	startedService.instance = currentInstance
	startedService.serviceStatus = &ServiceStatus{Status: ServiceStatus_STARTED}

	err := startedService.StartOrReloadService("{", nil)
	if err == nil {
		t.Fatal("expected invalid replacement config to fail")
	}
	if startedService.instance != currentInstance {
		t.Fatal("current instance was replaced after replacement preparation failed")
	}
	if startedService.serviceStatus.Status != ServiceStatus_STARTED {
		t.Fatalf("current service status changed to %s", startedService.serviceStatus.Status)
	}
}

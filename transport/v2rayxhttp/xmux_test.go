package v2rayxhttp

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing-box/option"
)

func TestXMuxDefaultConfig(t *testing.T) {
	config, err := newXMuxConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if config.maxConnections.from != 6 || config.maxConnections.to != 6 {
		t.Fatalf("unexpected default max_connections: %d-%d", config.maxConnections.from, config.maxConnections.to)
	}
	if config.hMaxRequestTimes.from != 600 || config.hMaxRequestTimes.to != 900 {
		t.Fatalf("unexpected default h_max_request_times: %d-%d", config.hMaxRequestTimes.from, config.hMaxRequestTimes.to)
	}
	if config.hMaxReusableSecs.from != 1800 || config.hMaxReusableSecs.to != 3000 {
		t.Fatalf("unexpected default h_max_reusable_secs: %d-%d", config.hMaxReusableSecs.from, config.hMaxReusableSecs.to)
	}
}

func TestXMuxRejectsConnectionAndConcurrency(t *testing.T) {
	_, err := newXMuxConfig(&option.V2RayXHTTPXMuxOptions{
		MaxConnections: &option.V2RayXHTTPRangeOptions{From: 2, To: 2},
		MaxConcurrency: &option.V2RayXHTTPRangeOptions{From: 2, To: 2},
	})
	if err == nil {
		t.Fatal("expected max_connections and max_concurrency conflict")
	}
}

func TestXMuxRequestLimitRotatesClient(t *testing.T) {
	manager := newXMuxManager(&xmuxConfig{
		hMaxRequestTimes: &rangeConfig{from: 1, to: 1},
	}, func() xmuxConn {
		return &testXMuxConn{}
	})
	first := manager.GetXMuxClient(context.Background())
	first.decrementRequests()
	second := manager.GetXMuxClient(context.Background())
	if first == second {
		t.Fatal("expected xmux client to rotate after request limit")
	}
}

type testXMuxConn struct {
	closed atomic.Bool
}

func (c *testXMuxConn) IsClosed() bool {
	return c.closed.Load()
}

func (c *testXMuxConn) Close() error {
	c.closed.Store(true)
	return nil
}

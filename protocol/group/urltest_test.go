package group

import (
	"context"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/log"
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

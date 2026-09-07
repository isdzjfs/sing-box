package urltest

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/stretchr/testify/require"
)

type testErrorDialer struct {
	dial func() (net.Conn, error)
}

func (d testErrorDialer) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return d.dial()
}

func (testErrorDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.ErrUnsupported
}

func TestURLTestReportsDialStage(t *testing.T) {
	sentinel := errors.New("sentinel")
	_, err := URLTest(context.Background(), "https://example.com/generate_204", testErrorDialer{
		dial: func() (net.Conn, error) { return nil, sentinel },
	})
	require.ErrorIs(t, err, sentinel)
	require.EqualError(t, err, "dial URL test target example.com:443: sentinel")
}

func TestURLTestReportsRequestStage(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	serverConn.Close()
	_, err := URLTest(context.Background(), "https://example.com/generate_204", testErrorDialer{
		dial: func() (net.Conn, error) { return clientConn, nil },
	})
	require.Error(t, err)
	require.ErrorContains(t, err, "perform URL test request to example.com:443")
}

func TestHistoryStorageKeepsRecentEntries(t *testing.T) {
	storage := NewHistoryStorage()
	baseTime := time.Unix(1000, 0)

	for i := 1; i <= 25; i++ {
		storage.StoreURLTestHistory("proxy", &adapter.URLTestHistory{
			Time:  baseTime.Add(time.Duration(i) * time.Second),
			Delay: uint16(i),
		})
	}

	histories := storage.LoadURLTestHistories("proxy")
	require.Len(t, histories, maxHistoryEntries)
	require.Equal(t, uint16(6), histories[0].Delay)
	require.Equal(t, uint16(25), histories[len(histories)-1].Delay)

	latest := storage.LoadURLTestHistory("proxy")
	require.NotNil(t, latest)
	require.Equal(t, uint16(25), latest.Delay)

	storage.StoreURLTestFailure("proxy", baseTime.Add(26*time.Second))

	histories = storage.LoadURLTestHistories("proxy")
	require.Len(t, histories, maxHistoryEntries)
	require.Equal(t, uint16(7), histories[0].Delay)
	require.Equal(t, uint16(0), histories[len(histories)-1].Delay)
	require.Nil(t, storage.LoadURLTestHistory("proxy"))

	storage.StoreURLTestHistory("proxy", &adapter.URLTestHistory{
		Time:  baseTime.Add(27 * time.Second),
		Delay: 27,
	})

	histories = storage.LoadURLTestHistories("proxy")
	require.Len(t, histories, maxHistoryEntries)
	require.Equal(t, uint16(8), histories[0].Delay)
	require.Equal(t, uint16(27), histories[len(histories)-1].Delay)

	latest = storage.LoadURLTestHistory("proxy")
	require.NotNil(t, latest)
	require.Equal(t, uint16(27), latest.Delay)
}

func TestHistoryStorageIsolatesTargetsAndReservations(t *testing.T) {
	storage := NewHistoryStorage()
	checkedAt := time.Now()
	first, second := "https://first.example/probe", "https://second.example/probe"
	require.True(t, storage.ReserveURLTest("shared", checkedAt, false, first))
	require.True(t, storage.ReserveURLTest("shared", checkedAt, false, second))
	success := storage.StoreURLTestHistory("shared", &adapter.URLTestHistory{Time: checkedAt, Delay: 42}, first)
	failure := storage.StoreURLTestFailure("shared", checkedAt, second)
	require.Same(t, success, storage.LoadURLTestHistory("shared", first))
	require.Nil(t, storage.LoadURLTestHistory("shared", second))
	require.Same(t, failure, storage.LoadLatestURLTestHistory("shared", second))
	require.Greater(t, failure.Sequence, success.Sequence)
	storage.FinishURLTest("shared", checkedAt, first)
	_, checking := storage.URLTestCheckingAt("shared", second)
	require.True(t, checking)
	storage.FinishURLTest("shared", checkedAt, second)
	_, healthy := storage.WaitURLTestResult(context.Background(), "shared", checkedAt, first)
	require.NoError(t, healthy)
	storage.DeleteURLTestHistory("shared", second)
	require.Same(t, success, storage.LoadURLTestHistory("shared", first))
}

func TestHistoryStorageEquivalentURLsShareScope(t *testing.T) {
	storage := NewHistoryStorage()
	checkedAt := time.Now()
	require.True(t, storage.ReserveURLTest("shared", checkedAt, false))
	require.False(t, storage.ReserveURLTest("shared", checkedAt, true, DefaultURL))
	success := storage.StoreURLTestHistory("shared", &adapter.URLTestHistory{Time: checkedAt, Delay: 10})
	require.Same(t, success, storage.LoadURLTestHistory("shared", "https://WWW.GSTATIC.COM:443/generate_204#unused"))
	require.Equal(t, "http://[::1]/", NormalizeURL("http://[::1]:80"))
	require.NotEqual(t, NormalizeURL("https://example.com/probe?a=1"), NormalizeURL("https://example.com/probe?a=2"))
}

func TestHistoryStorageFailureAndRepeatedTimestampHaveDistinctSequences(t *testing.T) {
	storage := NewHistoryStorage()
	checkedAt := time.Now()
	input := &adapter.URLTestHistory{Time: checkedAt, Delay: 42}
	success := storage.StoreURLTestHistory("node", input)
	failure := storage.StoreURLTestFailure("node", checkedAt)
	require.Zero(t, input.Sequence)
	require.Positive(t, success.Sequence)
	require.Greater(t, failure.Sequence, success.Sequence)
	require.Same(t, failure, storage.LoadLatestURLTestHistory("node"))
	require.Nil(t, storage.LoadURLTestHistory("node"))
}

func TestHistoryStorageDoesNotReplaceCurrentWithOlderResult(t *testing.T) {
	storage := NewHistoryStorage()
	baseTime := time.Unix(1000, 0)

	newer := &adapter.URLTestHistory{
		Time:  baseTime.Add(2 * time.Second),
		Delay: 20,
	}
	stored := storage.StoreURLTestHistory("proxy", newer)
	storage.StoreURLTestFailure("proxy", baseTime.Add(time.Second))

	current := storage.LoadURLTestHistory("proxy")
	require.Same(t, stored, current)
	require.Equal(t, newer.Time, current.Time)
	require.Equal(t, newer.Delay, current.Delay)

	histories := storage.LoadURLTestHistories("proxy")
	require.Len(t, histories, 2)
	require.Equal(t, baseTime.Add(time.Second), histories[0].Time)
	require.Equal(t, newer.Time, histories[1].Time)
}

func TestHistoryStorageDoesNotRestoreOlderSuccessAfterNewerFailure(t *testing.T) {
	storage := NewHistoryStorage()
	baseTime := time.Unix(1000, 0)

	storage.StoreURLTestFailure("proxy", baseTime.Add(2*time.Second))
	storage.StoreURLTestHistory("proxy", &adapter.URLTestHistory{
		Time:  baseTime.Add(time.Second),
		Delay: 10,
	})

	require.Nil(t, storage.LoadURLTestHistory("proxy"))
}

func TestHistoryStorageReserveURLTestSkipsDuplicateFailure(t *testing.T) {
	storage := NewHistoryStorage()
	baseTime := time.Unix(1000, 0)

	storage.StoreURLTestFailure("proxy", baseTime)

	require.False(t, storage.ReserveURLTest("proxy", baseTime.Add(500*time.Millisecond), false))
	require.True(t, storage.ReserveURLTest("proxy", baseTime.Add(30*time.Second), false))
}

func TestHistoryStorageReserveURLTestDeduplicatesActiveCheck(t *testing.T) {
	storage := NewHistoryStorage()
	baseTime := time.Unix(1000, 0)

	require.True(t, storage.ReserveURLTest("proxy", baseTime, false))
	require.False(t, storage.ReserveURLTest("proxy", baseTime, false))
	require.False(t, storage.ReserveURLTest("proxy", baseTime, true))

	storage.FinishURLTest("proxy", baseTime)
	require.True(t, storage.ReserveURLTest("proxy", baseTime, true))
}

func TestHistoryStorageReserveURLTestBlocksActiveCheckUntilFinished(t *testing.T) {
	storage := NewHistoryStorage()
	baseTime := time.Unix(1000, 0)

	require.True(t, storage.ReserveURLTest("proxy", baseTime, false))
	require.False(t, storage.ReserveURLTest("proxy", baseTime.Add(time.Hour), false))

	storage.FinishURLTest("proxy", baseTime)
	require.True(t, storage.ReserveURLTest("proxy", baseTime.Add(time.Hour), false))
}

func TestHistoryStorageReserveURLTestForceIgnoresRecentHistory(t *testing.T) {
	storage := NewHistoryStorage()
	baseTime := time.Unix(1000, 0)

	storage.StoreURLTestHistory("proxy", &adapter.URLTestHistory{
		Time:  baseTime,
		Delay: 100,
	})

	require.False(t, storage.ReserveURLTest("proxy", baseTime.Add(500*time.Millisecond), false))
	require.True(t, storage.ReserveURLTest("proxy", baseTime.Add(500*time.Millisecond), true))
}

func TestHistoryStorageReserveURLTestDoesNotStretchInterval(t *testing.T) {
	storage := NewHistoryStorage()
	baseTime := time.Unix(1000, 0)

	storage.StoreURLTestHistory("proxy", &adapter.URLTestHistory{
		Time:  baseTime,
		Delay: 100,
	})

	require.True(t, storage.ReserveURLTest("proxy", baseTime.Add(30*time.Second), false))
}

func TestHistoryStorageWaitURLTestResultReturnsActiveCheckResult(t *testing.T) {
	storage := NewHistoryStorage()
	checkedAt := time.Now()
	require.True(t, storage.ReserveURLTest("proxy", checkedAt, true))

	resultChannel := make(chan *adapter.URLTestHistory, 1)
	go func() {
		result, _ := storage.WaitURLTestResult(context.Background(), "proxy", checkedAt)
		resultChannel <- result
	}()
	storage.StoreURLTestHistory("proxy", &adapter.URLTestHistory{Time: checkedAt, Delay: 42})
	storage.FinishURLTest("proxy", checkedAt)

	result := <-resultChannel
	require.NotNil(t, result)
	require.Equal(t, uint16(42), result.Delay)
}

func TestHistoryStorageWaitURLTestResultHonorsContext(t *testing.T) {
	storage := NewHistoryStorage()
	checkedAt := time.Now()
	require.True(t, storage.ReserveURLTest("proxy", checkedAt, true))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := storage.WaitURLTestResult(ctx, "proxy", checkedAt)
	require.Nil(t, result)
	require.ErrorIs(t, err, context.Canceled)
}

func TestHistoryStorageWaitURLTestResultStopsWhenCheckFinishes(t *testing.T) {
	storage := NewHistoryStorage()
	checkedAt := time.Now()
	require.True(t, storage.ReserveURLTest("proxy", checkedAt, true))

	resultChannel := make(chan error, 1)
	go func() {
		_, err := storage.WaitURLTestResult(context.Background(), "proxy", checkedAt)
		resultChannel <- err
	}()
	storage.FinishURLTest("proxy", checkedAt)

	select {
	case err := <-resultChannel:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("waiting URL test did not stop")
	}
}

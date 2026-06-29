package urltest

import (
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/stretchr/testify/require"
)

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

func TestHistoryStorageReserveURLTestSkipsRecentFailure(t *testing.T) {
	storage := NewHistoryStorage()
	baseTime := time.Unix(1000, 0)

	storage.StoreURLTestFailure("proxy", baseTime)

	require.False(t, storage.ReserveURLTest("proxy", baseTime.Add(30*time.Second), time.Minute, false))
	require.True(t, storage.ReserveURLTest("proxy", baseTime.Add(time.Minute), time.Minute, false))
}

func TestHistoryStorageReserveURLTestDeduplicatesActiveCheck(t *testing.T) {
	storage := NewHistoryStorage()
	baseTime := time.Unix(1000, 0)

	require.True(t, storage.ReserveURLTest("proxy", baseTime, time.Minute, false))
	require.False(t, storage.ReserveURLTest("proxy", baseTime, time.Minute, false))
	require.False(t, storage.ReserveURLTest("proxy", baseTime, time.Minute, true))

	storage.FinishURLTest("proxy", baseTime)
	require.True(t, storage.ReserveURLTest("proxy", baseTime, time.Minute, true))
}

func TestHistoryStorageReserveURLTestForceIgnoresRecentHistory(t *testing.T) {
	storage := NewHistoryStorage()
	baseTime := time.Unix(1000, 0)

	storage.StoreURLTestHistory("proxy", &adapter.URLTestHistory{
		Time:  baseTime,
		Delay: 100,
	})

	require.False(t, storage.ReserveURLTest("proxy", baseTime.Add(30*time.Second), time.Minute, false))
	require.True(t, storage.ReserveURLTest("proxy", baseTime.Add(30*time.Second), time.Minute, true))
}

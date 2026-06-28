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
}

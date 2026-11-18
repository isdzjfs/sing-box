//go:build go1.24 && with_quic

package tls

import (
	"crypto/tls"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestECHRefreshKeepsInFlightQUICConfig(t *testing.T) {
	config := &ECHClientConfig{
		ECHCapableConfig: &STDClientConfig{config: &tls.Config{
			EncryptedClientHelloConfigList: []byte("first"),
		}},
		lastUpdate: time.Now(),
		lastTTL:    time.Hour,
	}
	first, err := config.fetchSTDConfig(t.Context())
	require.NoError(t, err)
	// A DNS refresh replaces the list while an earlier handshake still owns first.
	config.SetECHConfigList([]byte("second"))
	second, err := config.fetchSTDConfig(t.Context())
	require.NoError(t, err)
	require.Equal(t, []byte("first"), first.EncryptedClientHelloConfigList)
	require.Equal(t, []byte("second"), second.EncryptedClientHelloConfigList)
}

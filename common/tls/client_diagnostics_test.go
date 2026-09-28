package tls

import (
	stdTLS "crypto/tls"
	"errors"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClientHandshakeErrorIncludesSafeContext(t *testing.T) {
	config := &STDClientConfig{
		config: &stdTLS.Config{
			MinVersion: stdTLS.VersionTLS12,
			MaxVersion: stdTLS.VersionTLS13,
			NextProtos: []string{"h2", "http/1.1"},
		},
		serverName: "node.example",
	}
	conn := &tlsDiagnosticsTestConn{
		remoteAddr: &net.TCPAddr{IP: net.ParseIP("203.0.113.7"), Port: 443},
	}
	protocolErr := errors.New("remote error: tls: protocol version not supported")

	err := wrapClientHandshakeError(protocolErr, conn, config)

	require.ErrorIs(t, err, protocolErr)
	require.EqualError(t, err, "TLS handshake with \"203.0.113.7:443\" (server_name=\"node.example\", sni=enabled, engine=\"go\", client_hello=\"standard\", min_version=\"1.2\", max_version=\"1.3\", alpn=\"h2,http/1.1\"): remote error: tls: protocol version not supported")
}

func TestClientHandshakeContextShowsUnsetVersionLimits(t *testing.T) {
	config := &STDClientConfig{
		config:     &stdTLS.Config{},
		serverName: "node.example",
	}

	diagnostics := formatClientHandshakeContext(nil, config)

	require.Contains(t, diagnostics, `min_version="default"`)
	require.Contains(t, diagnostics, `max_version="default"`)
}

func TestClientHandshakeContextShowsOmittedSNIForIPAddress(t *testing.T) {
	config := &STDClientConfig{
		config:     &stdTLS.Config{ServerName: "203.0.113.7"},
		serverName: "203.0.113.7",
	}

	diagnostics := formatClientHandshakeContext(nil, config)

	require.Contains(t, diagnostics, `server_name="203.0.113.7"`)
	require.Contains(t, diagnostics, `sni=omitted`)
}

type tlsDiagnosticsTestConn struct {
	net.Conn
	remoteAddr net.Addr
}

func (c *tlsDiagnosticsTestConn) RemoteAddr() net.Addr {
	return c.remoteAddr
}

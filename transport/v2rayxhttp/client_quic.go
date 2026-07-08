//go:build with_quic

package v2rayxhttp

import (
	"context"
	stdTLS "crypto/tls"
	"net/http"
	"runtime"
	"time"

	"github.com/sagernet/quic-go"
	"github.com/sagernet/quic-go/http3"
	boxTLS "github.com/sagernet/sing-box/common/tls"
	"github.com/sagernet/sing/common/bufio"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

func newHTTP3RoundTripper(_ context.Context, dialer N.Dialer, serverAddr M.Socksaddr, tlsConfig boxTLS.Config, keepAlivePeriod time.Duration) (http.RoundTripper, error) {
	if tlsConfig == nil {
		return nil, E.New("xhttp HTTP/3 requires TLS")
	}
	stdTLSConfig, err := buildSTDTLSConfig(tlsConfig, serverAddr, []string{http3.NextProtoH3})
	if err != nil {
		return nil, err
	}
	if keepAlivePeriod == 0 {
		keepAlivePeriod = 10 * time.Second
	} else if keepAlivePeriod < 0 {
		keepAlivePeriod = 0
	}
	quicConfig := &quic.Config{
		MaxIdleTimeout:          v2rayHTTPIdleTimeout,
		KeepAlivePeriod:         keepAlivePeriod,
		MaxIncomingStreams:      -1,
		DisablePathMTUDiscovery: runtime.GOOS != "linux" && runtime.GOOS != "windows" && runtime.GOOS != "darwin",
	}
	if handshakeTimeout := tlsConfig.HandshakeTimeout(); handshakeTimeout > 0 {
		quicConfig.HandshakeIdleTimeout = handshakeTimeout
	}
	return &http3.Transport{
		TLSClientConfig: stdTLSConfig,
		QUICConfig:      quicConfig,
		Dial: func(ctx context.Context, addr string, tlsConfig *stdTLS.Config, quicConfig *quic.Config) (*quic.Conn, error) {
			conn, err := dialer.DialContext(ctx, N.NetworkUDP, serverAddr)
			if err != nil {
				return nil, err
			}
			quicConn, err := quic.DialEarly(ctx, bufio.NewUnbindPacketConn(conn), conn.RemoteAddr(), tlsConfig, quicConfig)
			if err != nil {
				conn.Close()
				return nil, err
			}
			return quicConn, nil
		},
	}, nil
}

func buildSTDTLSConfig(baseTLSConfig boxTLS.Config, destination M.Socksaddr, nextProtos []string) (*stdTLS.Config, error) {
	tlsConfig := baseTLSConfig.Clone()
	if tlsConfig.ServerName() == "" && destination.IsValid() {
		tlsConfig.SetServerName(destination.AddrString())
	}
	tlsConfig.SetNextProtos(nextProtos)
	return tlsConfig.STDConfig()
}

//go:build with_quic

package v2rayxhttp

import (
	"context"
	"net"
	"net/http"
	"runtime"

	"github.com/sagernet/quic-go"
	"github.com/sagernet/quic-go/http3"
	boxTLS "github.com/sagernet/sing-box/common/tls"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-quic"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
)

type quicHTTP3Server struct {
	ctx        context.Context
	logger     logger.ContextLogger
	tlsConfig  boxTLS.ServerConfig
	handler    http.Handler
	server     *http3.Server
	listener   http3.QUICListener
	packetConn net.PacketConn
}

func newHTTP3Server(ctx context.Context, logger logger.ContextLogger, tlsConfig boxTLS.ServerConfig, handler http.Handler) (http3Server, error) {
	err := qtls.ConfigureHTTP3(tlsConfig)
	if err != nil {
		return nil, err
	}
	if !common.Contains(tlsConfig.NextProtos(), http3.NextProtoH3) {
		tlsConfig.SetNextProtos(append(append([]string{}, tlsConfig.NextProtos()...), http3.NextProtoH3))
	}
	return &quicHTTP3Server{
		ctx:       ctx,
		logger:    logger,
		tlsConfig: tlsConfig,
		handler:   handler,
	}, nil
}

func (s *quicHTTP3Server) ServePacket(listener net.PacketConn) error {
	quicListener, err := qtls.ListenEarly(listener, s.tlsConfig, &quic.Config{
		MaxIdleTimeout:          v2rayHTTPIdleTimeout,
		MaxIncomingStreams:      1 << 60,
		Allow0RTT:               true,
		DisablePathMTUDiscovery: runtime.GOOS != "linux" && runtime.GOOS != "windows" && runtime.GOOS != "darwin",
	})
	if err != nil {
		listener.Close()
		return err
	}
	s.packetConn = listener
	s.listener = quicListener
	s.server = &http3.Server{
		Handler: s.handler,
		ConnContext: func(ctx context.Context, conn *quic.Conn) context.Context {
			return log.ContextWithNewID(ctx)
		},
	}
	go func() {
		err := s.server.ServeListener(quicListener)
		listener.Close()
		if err != nil && !E.IsClosedOrCanceled(err) {
			s.logger.ErrorContext(s.ctx, "xhttp http3 server closed: ", err)
		}
	}()
	return nil
}

func (s *quicHTTP3Server) Close() error {
	return common.Close(s.server, s.listener, s.packetConn)
}

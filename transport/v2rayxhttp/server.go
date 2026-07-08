package v2rayxhttp

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sagernet/sing-box/adapter"
	boxTLS "github.com/sagernet/sing-box/common/tls"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	aTLS "github.com/sagernet/sing/common/tls"
	sHTTP "github.com/sagernet/sing/protocol/http"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

var _ adapter.V2RayServerTransport = (*Server)(nil)

type Server struct {
	ctx        context.Context
	logger     logger.ContextLogger
	tlsConfig  boxTLS.ServerConfig
	handler    adapter.V2RayServerTransportHandler
	httpServer *http.Server
	h2Server   *http2.Server
	h2cHandler http.Handler
	h3Server   http3Server
	isH3       bool
	config     *config
	sessions   sync.Map
	sessionMu  sync.Mutex
}

type http3Server interface {
	ServePacket(listener net.PacketConn) error
	Close() error
}

type httpSession struct {
	uploadQueue      *uploadQueue
	fullyConnected   chan struct{}
	fullyConnectOnce sync.Once
}

func NewServer(ctx context.Context, logger logger.ContextLogger, options option.V2RayXHTTPOptions, tlsConfig boxTLS.ServerConfig, handler adapter.V2RayServerTransportHandler) (*Server, error) {
	transportConfig, err := newConfig(options)
	if err != nil {
		return nil, err
	}
	server := &Server{
		ctx:       ctx,
		logger:    logger,
		tlsConfig: tlsConfig,
		handler:   handler,
		config:    transportConfig,
		h2Server:  &http2.Server{},
	}
	server.isH3 = tlsConfig != nil && len(tlsConfig.NextProtos()) == 1 && tlsConfig.NextProtos()[0] == "h3"
	server.httpServer = &http.Server{
		Handler:           server,
		ReadHeaderTimeout: C.TCPTimeout,
		MaxHeaderBytes:    transportConfig.normalizedServerMaxHeaderBytes(),
		BaseContext: func(net.Listener) context.Context {
			return ctx
		},
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			return log.ContextWithNewID(ctx)
		},
	}
	server.h2cHandler = h2c.NewHandler(server, server.h2Server)
	if server.isH3 {
		server.h3Server, err = newHTTP3Server(ctx, logger, tlsConfig, server)
		if err != nil {
			return nil, err
		}
	}
	return server, nil
}

func (s *Server) Network() []string {
	if s.isH3 {
		return []string{N.NetworkUDP}
	}
	return []string{N.NetworkTCP}
}

func (s *Server) Serve(listener net.Listener) error {
	if s.isH3 {
		return os.ErrInvalid
	}
	if s.tlsConfig != nil {
		if len(s.tlsConfig.NextProtos()) == 0 {
			s.tlsConfig.SetNextProtos([]string{http2.NextProtoTLS, "http/1.1"})
		} else if !common.Contains(s.tlsConfig.NextProtos(), http2.NextProtoTLS) {
			s.tlsConfig.SetNextProtos(append([]string{http2.NextProtoTLS}, s.tlsConfig.NextProtos()...))
		}
		listener = aTLS.NewListener(listener, s.tlsConfig)
	}
	return s.httpServer.Serve(listener)
}

func (s *Server) ServePacket(listener net.PacketConn) error {
	if s.isH3 {
		return s.h3Server.ServePacket(listener)
	}
	return os.ErrInvalid
}

func (s *Server) Close() error {
	return common.Close(common.PtrOrNil(s.httpServer), s.h3Server)
}

func (s *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method == "PRI" && len(request.Header) == 0 && request.URL.Path == "*" && request.Proto == "HTTP/2.0" {
		s.h2cHandler.ServeHTTP(writer, request)
		return
	}
	path := s.config.normalizedPath()
	if !matchHost(request.Host, s.config.host) {
		s.invalidRequest(writer, request, http.StatusNotFound, E.New("bad host: ", request.Host))
		return
	}
	if !strings.HasPrefix(request.URL.Path, path) {
		s.invalidRequest(writer, request, http.StatusNotFound, E.New("bad path: ", request.URL.Path))
		return
	}
	s.writeResponseHeader(writer, request)
	s.config.applyResponsePadding(writer)
	if request.Method == http.MethodOptions {
		writer.WriteHeader(http.StatusOK)
		return
	}
	paddingValue := s.config.extractXPaddingFromRequest(request)
	if !s.config.isPaddingValid(paddingValue) {
		s.invalidRequest(writer, request, http.StatusBadRequest, E.New("invalid xhttp padding"))
		return
	}
	obfsPaddingAccepted := s.config.xPaddingObfsMode && paddingValue != ""
	sessionID, seq := s.config.extractMetaFromRequest(request, path)
	if sessionID == "" && s.config.mode != "" && s.config.mode != modeAuto && s.config.mode != modeStreamOne && s.config.mode != modeStreamUp {
		s.invalidRequest(writer, request, http.StatusBadRequest, E.New("stream-one mode is not allowed"))
		return
	}
	var session *httpSession
	if sessionID != "" {
		session = s.upsertSession(sessionID)
	}
	isUplinkRequest := request.Method != http.MethodGet || seq != ""
	if isUplinkRequest && sessionID != "" {
		s.handleUpload(writer, request, session, seq, obfsPaddingAccepted)
		return
	}
	if request.Method == http.MethodGet || sessionID == "" {
		s.handleDownload(writer, request, sessionID, session)
		return
	}
	s.invalidRequest(writer, request, http.StatusMethodNotAllowed, E.New("unsupported method: ", request.Method))
}

func (s *Server) handleUpload(writer http.ResponseWriter, request *http.Request, session *httpSession, seq string, obfsPaddingAccepted bool) {
	if seq == "" {
		if s.config.mode != "" && s.config.mode != modeAuto && s.config.mode != modeStreamUp {
			s.invalidRequest(writer, request, http.StatusBadRequest, E.New("stream-up mode is not allowed"))
			return
		}
		httpConn := &serverHTTPConn{
			done:           make(chan struct{}),
			reader:         request.Body,
			responseWriter: writer,
		}
		if err := session.uploadQueue.Push(packet{reader: httpConn}); err != nil {
			s.invalidRequest(writer, request, http.StatusConflict, E.Cause(err, "push upload reader"))
			return
		}
		writer.Header().Set("X-Accel-Buffering", "no")
		writer.Header().Set("Cache-Control", "no-store")
		writer.WriteHeader(http.StatusOK)
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
		scStreamUpServerSecs := s.config.normalizedScStreamUpServerSecs()
		if (request.Header.Get("Referer") != "" || obfsPaddingAccepted) && scStreamUpServerSecs.to > 0 {
			go func() {
				for {
					_, writeErr := httpConn.Write([]byte(generatePadding(paddingMethodRepeatX, int(s.config.normalizedXPaddingBytes().rand()))))
					if writeErr != nil {
						return
					}
					time.Sleep(time.Duration(scStreamUpServerSecs.rand()) * time.Second)
				}
			}()
		}
		select {
		case <-request.Context().Done():
		case <-httpConn.done:
		}
		httpConn.Close()
		return
	}
	if s.config.mode != "" && s.config.mode != modeAuto && s.config.mode != modePacketUp {
		s.invalidRequest(writer, request, http.StatusBadRequest, E.New("packet-up mode is not allowed"))
		return
	}
	payload, err := s.readPacketPayload(request)
	if err != nil {
		s.invalidRequest(writer, request, http.StatusBadRequest, err)
		return
	}
	if len(payload) > int(s.config.normalizedScMaxEachPostBytes().to) {
		s.invalidRequest(writer, request, http.StatusRequestEntityTooLarge, E.New("xhttp upload is too large"))
		return
	}
	seqNumber, err := strconv.ParseUint(seq, 10, 64)
	if err != nil {
		s.invalidRequest(writer, request, http.StatusBadRequest, E.Cause(err, "parse seq"))
		return
	}
	if err := session.uploadQueue.Push(packet{payload: payload, seq: seqNumber}); err != nil {
		s.invalidRequest(writer, request, http.StatusInternalServerError, E.Cause(err, "push upload payload"))
		return
	}
	if len(payload) == 0 {
		writer.Header().Set("Cache-Control", "no-store")
	}
	writer.WriteHeader(http.StatusOK)
}

func (s *Server) handleDownload(writer http.ResponseWriter, request *http.Request, sessionID string, session *httpSession) {
	if sessionID != "" {
		session.fullyConnectOnce.Do(func() {
			close(session.fullyConnected)
		})
		defer s.sessions.Delete(sessionID)
	}
	writer.Header().Set("X-Accel-Buffering", "no")
	writer.Header().Set("Cache-Control", "no-store")
	if !s.config.noSSEHeader {
		writer.Header().Set("Content-Type", "text/event-stream")
	}
	writer.WriteHeader(http.StatusOK)
	if flusher, ok := writer.(http.Flusher); ok {
		flusher.Flush()
	}
	httpConn := &serverHTTPConn{
		done:           make(chan struct{}),
		reader:         request.Body,
		responseWriter: writer,
	}
	conn := &splitConn{
		writer:     httpConn,
		reader:     httpConn,
		remoteAddr: sourceAddress(request),
		localAddr:  localAddress(request),
	}
	if sessionID != "" {
		conn.reader = session.uploadQueue
	}
	s.handler.NewConnectionEx(request.Context(), conn, sHTTP.SourceAddress(request), M.Socksaddr{}, N.OnceClose(func(error) {
		httpConn.Close()
	}))
	select {
	case <-request.Context().Done():
	case <-httpConn.done:
	}
	conn.Close()
}

func (s *Server) upsertSession(sessionID string) *httpSession {
	if sessionValue, loaded := s.sessions.Load(sessionID); loaded {
		return sessionValue.(*httpSession)
	}
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	if sessionValue, loaded := s.sessions.Load(sessionID); loaded {
		return sessionValue.(*httpSession)
	}
	session := &httpSession{
		uploadQueue:    newUploadQueue(s.config.normalizedScMaxBufferedPosts()),
		fullyConnected: make(chan struct{}),
	}
	s.sessions.Store(sessionID, session)
	go func() {
		select {
		case <-time.After(30 * time.Second):
			s.sessions.Delete(sessionID)
			session.uploadQueue.Close()
		case <-session.fullyConnected:
		}
	}()
	return session
}

func (s *Server) readPacketPayload(request *http.Request) ([]byte, error) {
	dataPlacement := s.config.uplinkDataPlacement
	var payload []byte
	if dataPlacement == placementAuto || dataPlacement == placementHeader {
		headerPayload, err := readChunkedHeaderPayload(request.Header, s.config.uplinkDataKey)
		if err != nil {
			return nil, err
		}
		payload = append(payload, headerPayload...)
	}
	if dataPlacement == placementAuto || dataPlacement == placementCookie {
		cookiePayload, err := readChunkedCookiePayload(request, s.config.uplinkDataKey)
		if err != nil {
			return nil, err
		}
		payload = append(payload, cookiePayload...)
	}
	if dataPlacement == placementAuto || dataPlacement == placementBody {
		if request.ContentLength > int64(s.config.normalizedScMaxEachPostBytes().to) {
			return nil, E.New("xhttp upload is too large")
		}
		bodyPayload, err := io.ReadAll(io.LimitReader(request.Body, int64(s.config.normalizedScMaxEachPostBytes().to)+1))
		if err != nil {
			return nil, E.Cause(err, "read body payload")
		}
		payload = append(payload, bodyPayload...)
	}
	return payload, nil
}

func readChunkedHeaderPayload(header http.Header, key string) ([]byte, error) {
	var encodedChunks []string
	for i := 0; ; i++ {
		chunk := header.Get(fmt.Sprintf("%s-%d", key, i))
		if chunk == "" {
			break
		}
		encodedChunks = append(encodedChunks, chunk)
	}
	if len(encodedChunks) == 0 {
		return nil, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.Join(encodedChunks, ""))
	if err != nil {
		return nil, E.Cause(err, "decode header payload")
	}
	return payload, nil
}

func readChunkedCookiePayload(request *http.Request, key string) ([]byte, error) {
	var encodedChunks []string
	for i := 0; ; i++ {
		cookie, err := request.Cookie(fmt.Sprintf("%s_%d", key, i))
		if err != nil || cookie == nil {
			break
		}
		encodedChunks = append(encodedChunks, cookie.Value)
	}
	if len(encodedChunks) == 0 {
		return nil, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.Join(encodedChunks, ""))
	if err != nil {
		return nil, E.Cause(err, "decode cookie payload")
	}
	return payload, nil
}

func (s *Server) writeResponseHeader(writer http.ResponseWriter, request *http.Request) {
	if origin := request.Header.Get("Origin"); origin == "" {
		writer.Header().Set("Access-Control-Allow-Origin", "*")
	} else {
		writer.Header().Set("Access-Control-Allow-Origin", origin)
	}
	if s.config.sessionIDPlacement == placementCookie ||
		s.config.seqPlacement == placementCookie ||
		s.config.xPaddingPlacement == placementCookie ||
		s.config.uplinkDataPlacement == placementCookie {
		writer.Header().Set("Access-Control-Allow-Credentials", "true")
	}
	if request.Method == http.MethodOptions {
		if requestedMethod := request.Header.Get("Access-Control-Request-Method"); requestedMethod != "" {
			writer.Header().Set("Access-Control-Allow-Methods", requestedMethod)
		} else {
			writer.Header().Set("Access-Control-Allow-Methods", "*")
		}
		if requestedHeaders := request.Header.Get("Access-Control-Request-Headers"); requestedHeaders != "" {
			writer.Header().Set("Access-Control-Allow-Headers", requestedHeaders)
		} else {
			writer.Header().Set("Access-Control-Allow-Headers", "*")
		}
	}
}

func (s *Server) invalidRequest(writer http.ResponseWriter, request *http.Request, statusCode int, err error) {
	if statusCode > 0 {
		writer.WriteHeader(statusCode)
	}
	s.logger.ErrorContext(request.Context(), E.Cause(err, "process connection from ", request.RemoteAddr))
}

type serverHTTPConn struct {
	sync.Mutex
	done           chan struct{}
	doneOnce       sync.Once
	reader         io.Reader
	responseWriter http.ResponseWriter
}

func (c *serverHTTPConn) Read(b []byte) (int, error) {
	return c.reader.Read(b)
}

func (c *serverHTTPConn) Write(b []byte) (int, error) {
	c.Lock()
	defer c.Unlock()
	select {
	case <-c.done:
		return 0, io.ErrClosedPipe
	default:
	}
	n, err := c.responseWriter.Write(b)
	if err == nil {
		c.responseWriter.(http.Flusher).Flush()
	}
	return n, err
}

func (c *serverHTTPConn) Close() error {
	c.doneOnce.Do(func() {
		close(c.done)
	})
	return nil
}

func sourceAddress(request *http.Request) net.Addr {
	if request.ProtoMajor == 3 {
		addr, err := net.ResolveUDPAddr("udp", request.RemoteAddr)
		if err == nil {
			return addr
		}
	}
	addr, err := net.ResolveTCPAddr("tcp", request.RemoteAddr)
	if err != nil {
		return emptyAddr{}
	}
	if request.ProtoMajor == 3 {
		return &net.UDPAddr{IP: addr.IP, Port: addr.Port}
	}
	return addr
}

func localAddress(request *http.Request) net.Addr {
	if localAddr, loaded := request.Context().Value(http.LocalAddrContextKey).(net.Addr); loaded {
		return localAddr
	}
	return emptyAddr{}
}

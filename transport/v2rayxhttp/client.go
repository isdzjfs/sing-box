package v2rayxhttp

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/adapter"
	boxDialer "github.com/sagernet/sing-box/common/dialer"
	boxTLS "github.com/sagernet/sing-box/common/tls"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/transport/v2rayhttp"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"golang.org/x/net/http2"
)

var _ adapter.V2RayClientTransport = (*Client)(nil)

type Client struct {
	ctx      context.Context
	config   *config
	primary  *clientEndpoint
	download *clientEndpoint
}

type clientEndpoint struct {
	ctx         context.Context
	dialer      N.Dialer
	serverAddr  M.Socksaddr
	config      *config
	tlsConfig   boxTLS.Config
	httpVersion string
	isReality   bool
	requestURL  url.URL
	manager     *xmuxManager
}

type dialerClient interface {
	xmuxConn
	OpenStream(context.Context, string, string, io.Reader, bool) (io.ReadCloser, net.Addr, net.Addr, error)
	PostPacket(context.Context, string, string, string, []byte) error
}

type xhttpClient struct {
	config      *config
	transport   http.RoundTripper
	httpVersion string
	closed      atomic.Bool
}

func NewClient(ctx context.Context, dialer N.Dialer, serverAddr M.Socksaddr, options option.V2RayXHTTPOptions, tlsConfig boxTLS.Config) (*Client, error) {
	transportConfig, err := newConfig(options)
	if err != nil {
		return nil, err
	}
	primary, err := newClientEndpoint(ctx, dialer, serverAddr, transportConfig, tlsConfig)
	if err != nil {
		return nil, err
	}
	var download *clientEndpoint
	if options.DownloadSettings != nil {
		download, err = newDownloadEndpoint(ctx, dialer, serverAddr, transportConfig, tlsConfig, *options.DownloadSettings)
		if err != nil {
			return nil, err
		}
	}
	return &Client{
		ctx:      ctx,
		config:   transportConfig,
		primary:  primary,
		download: download,
	}, nil
}

func newClientEndpoint(ctx context.Context, dialer N.Dialer, serverAddr M.Socksaddr, transportConfig *config, tlsConfig boxTLS.Config) (*clientEndpoint, error) {
	endpoint := &clientEndpoint{
		ctx:         ctx,
		dialer:      dialer,
		serverAddr:  serverAddr,
		config:      transportConfig,
		tlsConfig:   tlsConfig,
		httpVersion: decideHTTPVersion(tlsConfig),
		isReality:   isRealityClientTLS(tlsConfig),
	}
	endpoint.requestURL = buildRequestURL(serverAddr, transportConfig, tlsConfig)
	endpoint.manager = newXMuxManager(transportConfig.xmux, func() xmuxConn {
		httpClient, err := newXHTTPClient(ctx, dialer, serverAddr, transportConfig, tlsConfig, endpoint.httpVersion)
		if err != nil {
			return failedHTTPClient{err: err}
		}
		return httpClient
	})
	return endpoint, nil
}

func newDownloadEndpoint(ctx context.Context, dialer N.Dialer, serverAddr M.Socksaddr, parentConfig *config, parentTLS boxTLS.Config, settings option.V2RayXHTTPDownloadOptions) (*clientEndpoint, error) {
	downloadServer := serverAddr
	if settings.Server != "" || settings.ServerPort != 0 {
		server := settings.Server
		if server == "" {
			server = serverAddr.AddrString()
		}
		port := settings.ServerPort
		if port == 0 {
			port = serverAddr.Port
		}
		downloadServer = M.ParseSocksaddrHostPort(server, port)
	} else if settings.Address != "" || settings.Port != 0 {
		server := settings.Address
		if server == "" {
			server = serverAddr.AddrString()
		}
		port := settings.Port
		if port == 0 {
			port = serverAddr.Port
		}
		downloadServer = M.ParseSocksaddrHostPort(server, port)
	}
	downloadDialer := dialer
	if !reflect.DeepEqual(settings.DialerOptions, option.DialerOptions{}) {
		var err error
		downloadDialer, err = boxDialer.New(ctx, settings.DialerOptions, downloadServer.IsDomain())
		if err != nil {
			return nil, err
		}
	}
	downloadConfig := parentConfig
	if settings.Transport != nil || settings.XHTTPSettings != nil || settings.SplitHTTPSettings != nil || settings.Network != "" {
		transportOptions, err := downloadTransportOptions(settings)
		if err != nil {
			return nil, err
		}
		if transportOptions.Type != C.V2RayTransportTypeXHTTP && transportOptions.Type != C.V2RayTransportTypeSplitHTTP {
			return nil, E.New("xhttp download_settings only supports xhttp transport")
		}
		downloadOptions := transportOptions.XHTTPOptions
		downloadOptions.DownloadSettings = nil
		downloadConfig, err = newConfig(downloadOptions)
		if err != nil {
			return nil, E.Cause(err, "create xhttp download_settings transport")
		}
	}
	downloadTLS := parentTLS
	if tlsOptions, loaded, err := downloadTLSOptions(settings); err != nil {
		return nil, err
	} else if loaded {
		var err error
		if tlsOptions.Enabled {
			downloadTLS, err = boxTLS.NewClientWithOptions(boxTLS.ClientOptions{
				Context:       ctx,
				Logger:        logger.NOP(),
				ServerAddress: downloadServer.AddrString(),
				Options:       tlsOptions,
			})
			if err != nil {
				return nil, err
			}
		} else {
			downloadTLS = nil
		}
	}
	return newClientEndpoint(ctx, downloadDialer, downloadServer, downloadConfig, downloadTLS)
}

func downloadTransportOptions(settings option.V2RayXHTTPDownloadOptions) (option.V2RayTransportOptions, error) {
	if settings.Transport != nil {
		return *settings.Transport, nil
	}
	transportType := settings.Network
	if transportType == "" {
		transportType = C.V2RayTransportTypeXHTTP
	}
	switch transportType {
	case C.V2RayTransportTypeXHTTP, C.V2RayTransportTypeSplitHTTP:
	default:
		return option.V2RayTransportOptions{}, E.New("unsupported xhttp download_settings network: ", transportType)
	}
	xhttpOptions := option.V2RayXHTTPOptions{}
	if settings.XHTTPSettings != nil {
		xhttpOptions = *settings.XHTTPSettings
	} else if settings.SplitHTTPSettings != nil {
		xhttpOptions = *settings.SplitHTTPSettings
	}
	return option.V2RayTransportOptions{
		Type:         transportType,
		XHTTPOptions: xhttpOptions,
	}, nil
}

func downloadTLSOptions(settings option.V2RayXHTTPDownloadOptions) (option.OutboundTLSOptions, bool, error) {
	if settings.TLS != nil {
		return *settings.TLS, true, nil
	}
	switch settings.Security {
	case "none":
		return option.OutboundTLSOptions{}, true, nil
	case "tls", "":
		if settings.TLSSettings == nil && settings.Security == "" {
			break
		}
		tlsOptions := option.OutboundTLSOptions{Enabled: true}
		if settings.TLSSettings != nil {
			tlsOptions.ServerName = settings.TLSSettings.ServerName
			tlsOptions.ALPN = settings.TLSSettings.ALPN
			tlsOptions.Insecure = settings.TLSSettings.AllowInsecure
			if settings.TLSSettings.Fingerprint != "" {
				tlsOptions.UTLS = &option.OutboundUTLSOptions{
					Enabled:     true,
					Fingerprint: settings.TLSSettings.Fingerprint,
				}
			}
		}
		return tlsOptions, true, nil
	case "reality":
		tlsOptions := option.OutboundTLSOptions{
			Enabled: true,
			Reality: &option.OutboundRealityOptions{
				Enabled: true,
			},
		}
		if settings.RealitySettings != nil {
			tlsOptions.ServerName = settings.RealitySettings.ServerName
			tlsOptions.Reality.PublicKey = settings.RealitySettings.PublicKey
			tlsOptions.Reality.ShortID = settings.RealitySettings.ShortID
			if settings.RealitySettings.Fingerprint != "" {
				tlsOptions.UTLS = &option.OutboundUTLSOptions{
					Enabled:     true,
					Fingerprint: settings.RealitySettings.Fingerprint,
				}
			}
		}
		return tlsOptions, true, nil
	default:
		return option.OutboundTLSOptions{}, false, E.New("unsupported xhttp download_settings security: ", settings.Security)
	}
	return option.OutboundTLSOptions{}, false, nil
}

func buildRequestURL(serverAddr M.Socksaddr, transportConfig *config, tlsConfig boxTLS.Config) url.URL {
	var requestURL url.URL
	if tlsConfig == nil {
		requestURL.Scheme = "http"
	} else {
		requestURL.Scheme = "https"
	}
	requestURL.Host = transportConfig.host
	if requestURL.Host == "" && tlsConfig != nil {
		requestURL.Host = tlsConfig.ServerName()
	}
	if requestURL.Host == "" {
		requestURL.Host = serverAddr.AddrString()
	}
	requestURL.Path = transportConfig.normalizedPath()
	requestURL.RawQuery = transportConfig.normalizedQuery()
	return requestURL
}

func newXHTTPClient(ctx context.Context, dialer N.Dialer, serverAddr M.Socksaddr, transportConfig *config, tlsConfig boxTLS.Config, httpVersion string) (*xhttpClient, error) {
	transport, err := newHTTPRoundTripper(ctx, dialer, serverAddr, transportConfig, tlsConfig, httpVersion)
	if err != nil {
		return nil, err
	}
	return &xhttpClient{
		config:      transportConfig,
		transport:   transport,
		httpVersion: httpVersion,
	}, nil
}

func newHTTPRoundTripper(ctx context.Context, dialer N.Dialer, serverAddr M.Socksaddr, transportConfig *config, tlsConfig boxTLS.Config, httpVersion string) (http.RoundTripper, error) {
	keepAlivePeriod := transportConfig.xmux.httpKeepAlivePeriod()
	if httpVersion == "3" {
		return newHTTP3RoundTripper(ctx, dialer, serverAddr, tlsConfig, keepAlivePeriod)
	}
	if tlsConfig == nil {
		return &http.Transport{
			DialContext: func(ctx context.Context, network string, addr string) (net.Conn, error) {
				return dialer.DialContext(ctx, network, serverAddr)
			},
			IdleConnTimeout:   v2rayHTTPIdleTimeout,
			DisableKeepAlives: true,
		}, nil
	}
	tlsDialer := boxTLS.NewDialer(dialer, tlsConfig)
	if httpVersion == "1.1" {
		return &http.Transport{
			DialTLSContext: func(ctx context.Context, network string, addr string) (net.Conn, error) {
				return tlsDialer.DialTLSContext(ctx, serverAddr)
			},
			IdleConnTimeout:   v2rayHTTPIdleTimeout,
			DisableKeepAlives: true,
			ForceAttemptHTTP2: false,
		}, nil
	}
	if len(tlsConfig.NextProtos()) == 0 {
		tlsConfig.SetNextProtos([]string{http2.NextProtoTLS})
	}
	h2KeepAlivePeriod := chromeH2KeepAlivePeriod
	if keepAlivePeriod > 0 {
		h2KeepAlivePeriod = keepAlivePeriod
	} else if keepAlivePeriod < 0 {
		h2KeepAlivePeriod = 0
	}
	return &http2.Transport{
		DialTLSContext: func(ctx context.Context, network string, addr string, cfg *boxTLS.STDConfig) (net.Conn, error) {
			return tlsDialer.DialTLSContext(ctx, serverAddr)
		},
		IdleConnTimeout: v2rayHTTPIdleTimeout,
		ReadIdleTimeout: h2KeepAlivePeriod,
	}, nil
}

func (e *clientEndpoint) getHTTPClient(ctx context.Context) (dialerClient, *xmuxClient) {
	xmuxClient := e.manager.GetXMuxClient(ctx)
	return xmuxClient.xmuxConn.(dialerClient), xmuxClient
}

func (c *Client) DialContext(ctx context.Context) (net.Conn, error) {
	mode := c.config.mode
	if mode == "" || mode == modeAuto {
		if c.primary.isReality {
			mode = modeStreamOne
			if c.download != nil {
				mode = modeStreamUp
			}
		} else {
			mode = modePacketUp
		}
	}
	requestCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	reader, writer := io.Pipe()
	primaryClient, primaryXMux := c.primary.getHTTPClient(requestCtx)
	downloadEndpoint := c.primary
	if c.download != nil {
		downloadEndpoint = c.download
	}
	downloadClient := primaryClient
	downloadXMux := primaryXMux
	if downloadEndpoint != c.primary {
		downloadClient, downloadXMux = downloadEndpoint.getHTTPClient(requestCtx)
	}
	if primaryXMux != nil {
		primaryXMux.AddRunning()
	}
	if downloadXMux != nil && downloadXMux != primaryXMux {
		downloadXMux.AddRunning()
	}
	var released atomic.Bool
	release := func() {
		if released.Swap(true) {
			return
		}
		cancel()
		if primaryXMux != nil {
			primaryXMux.DoneRunning()
		}
		if downloadXMux != nil && downloadXMux != primaryXMux {
			downloadXMux.DoneRunning()
		}
	}
	conn := &splitConn{
		writer:  writer,
		onClose: release,
	}
	if mode == modeStreamOne {
		if primaryXMux != nil {
			primaryXMux.decrementRequests()
		}
		readCloser, remoteAddr, localAddr, err := primaryClient.OpenStream(requestCtx, c.primary.requestURL.String(), "", reader, false)
		if err != nil {
			release()
			reader.Close()
			writer.Close()
			return nil, err
		}
		conn.reader = readCloser
		conn.remoteAddr = remoteAddr
		conn.localAddr = localAddr
		return conn, nil
	}
	sessionID := c.config.generateSessionID()
	if downloadXMux != nil {
		downloadXMux.decrementRequests()
	}
	readCloser, remoteAddr, localAddr, err := downloadClient.OpenStream(requestCtx, downloadEndpoint.requestURL.String(), sessionID, nil, false)
	if err != nil {
		release()
		reader.Close()
		writer.Close()
		return nil, err
	}
	conn.reader = readCloser
	conn.remoteAddr = remoteAddr
	conn.localAddr = localAddr
	if mode == modeStreamUp {
		if primaryXMux != nil {
			primaryXMux.decrementRequests()
		}
		_, _, _, err = primaryClient.OpenStream(requestCtx, c.primary.requestURL.String(), sessionID, reader, true)
		if err != nil {
			release()
			readCloser.Close()
			reader.Close()
			writer.Close()
			return nil, err
		}
		return conn, nil
	}
	go c.postPacketLoop(requestCtx, reader, sessionID, c.primary, primaryClient, primaryXMux)
	return conn, nil
}

func (c *Client) postPacketLoop(ctx context.Context, reader io.Reader, sessionID string, endpoint *clientEndpoint, dynamicClient dialerClient, dynamicXMux *xmuxClient) {
	maxUploadSize := int(c.config.normalizedScMaxEachPostBytes().rand())
	if maxUploadSize <= 0 {
		maxUploadSize = 1
	}
	buffer := make([]byte, maxUploadSize)
	var seq int64
	var lastWrite time.Time
	for {
		n, err := reader.Read(buffer)
		if n > 0 {
			minInterval := c.config.normalizedScMinPostsIntervalMs()
			if minInterval.from > 0 {
				delay := time.Duration(minInterval.rand())*time.Millisecond - time.Since(lastWrite)
				if delay > 0 {
					time.Sleep(delay)
				}
			}
			lastWrite = time.Now()
			if dynamicXMux != nil &&
				(dynamicXMux.decrementRequests() <= 0 ||
					(!dynamicXMux.unreusableAt.IsZero() && lastWrite.After(dynamicXMux.unreusableAt))) {
				dynamicClient, dynamicXMux = endpoint.getHTTPClient(ctx)
			}
			payload := make([]byte, n)
			copy(payload, buffer[:n])
			postErr := dynamicClient.PostPacket(ctx, endpoint.requestURL.String(), sessionID, strconv.FormatInt(seq, 10), payload)
			seq++
			if postErr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func (c *xhttpClient) IsClosed() bool {
	return c.closed.Load()
}

func (c *xhttpClient) OpenStream(ctx context.Context, rawURL string, sessionID string, body io.Reader, uploadOnly bool) (io.ReadCloser, net.Addr, net.Addr, error) {
	var remoteAddr net.Addr
	var localAddr net.Addr
	gotConn := make(chan struct{})
	var gotConnOnce sync.Once
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		GotConn: func(connInfo httptrace.GotConnInfo) {
			remoteAddr = connInfo.Conn.RemoteAddr()
			localAddr = connInfo.Conn.LocalAddr()
			gotConnOnce.Do(func() {
				close(gotConn)
			})
		},
	})
	method := http.MethodGet
	var requestBody io.ReadCloser
	if body != nil {
		method = c.config.uplinkHTTPMethod
		requestBody = io.NopCloser(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, rawURL, requestBody)
	if err != nil {
		return nil, nil, nil, err
	}
	c.config.fillStreamRequest(request, sessionID)
	waitReader := newWaitReadCloser()
	go func() {
		response, err := c.roundTrip(request)
		if err != nil {
			gotConnOnce.Do(func() {
				close(gotConn)
			})
			common.Close(body)
			waitReader.Close()
			return
		}
		if response.StatusCode != http.StatusOK || uploadOnly {
			_, _ = io.Copy(io.Discard, response.Body)
			response.Body.Close()
			common.Close(body)
			waitReader.Close()
			return
		}
		waitReader.Set(response.Body)
	}()
	<-gotConn
	return waitReader, remoteAddr, localAddr, nil
}

func (c *xhttpClient) PostPacket(ctx context.Context, rawURL string, sessionID string, seq string, payload []byte) error {
	request, err := http.NewRequestWithContext(ctx, c.config.uplinkHTTPMethod, rawURL, nil)
	if err != nil {
		return err
	}
	c.config.fillPacketRequest(request, sessionID, seq, payload)
	response, err := c.roundTrip(request)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, response.Body)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return E.New("v2ray-xhttp: unexpected status: ", response.Status)
	}
	return nil
}

func (c *xhttpClient) roundTrip(request *http.Request) (*http.Response, error) {
	response, err := c.transport.RoundTrip(request)
	if err != nil {
		c.closed.Store(true)
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, E.New("v2ray-xhttp: unexpected status: ", response.Status)
	}
	return response, nil
}

func (c *xhttpClient) Close() error {
	c.closed.Store(true)
	v2rayhttp.CloseIdleConnections(c.transport)
	if closer, ok := c.transport.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}

func (c *Client) Close() error {
	var err error
	if c.primary != nil && c.primary.manager != nil {
		err = c.primary.manager.Close()
	}
	if c.download != nil && c.download != c.primary && c.download.manager != nil {
		if closeErr := c.download.manager.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}
	return err
}

type failedHTTPClient struct {
	err error
}

func (c failedHTTPClient) IsClosed() bool {
	return true
}

func (c failedHTTPClient) OpenStream(context.Context, string, string, io.Reader, bool) (io.ReadCloser, net.Addr, net.Addr, error) {
	return nil, nil, nil, c.err
}

func (c failedHTTPClient) PostPacket(context.Context, string, string, string, []byte) error {
	return c.err
}

func (c failedHTTPClient) Close() error {
	return nil
}

func decideHTTPVersion(tlsConfig boxTLS.Config) string {
	if isRealityClientTLS(tlsConfig) {
		return "2"
	}
	if tlsConfig == nil {
		return "1.1"
	}
	nextProtos := tlsConfig.NextProtos()
	if len(nextProtos) != 1 {
		return "2"
	}
	switch nextProtos[0] {
	case "http/1.1":
		return "1.1"
	case "h3":
		return "3"
	default:
		return "2"
	}
}

func isRealityClientTLS(tlsConfig boxTLS.Config) bool {
	if tlsConfig == nil {
		return false
	}
	return strings.Contains(fmt.Sprintf("%T", tlsConfig), "RealityClientConfig") ||
		strings.Contains(reflect.TypeOf(tlsConfig).String(), "RealityClientConfig")
}

const (
	v2rayHTTPIdleTimeout    = 90 * time.Second
	chromeH2KeepAlivePeriod = 45 * time.Second
)

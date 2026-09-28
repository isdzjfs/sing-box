package tls

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"os"
	"reflect"
	"strconv"
	"strings"

	"github.com/sagernet/sing-box/common/badtls"
	"github.com/sagernet/sing-box/common/tlsspoof"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	aTLS "github.com/sagernet/sing/common/tls"
)

var errMissingServerName = E.New("missing server_name or insecure=true")

func parseTLSSpoofOptions(serverName string, options option.OutboundTLSOptions) (string, tlsspoof.Method, error) {
	spoof, method, err := tlsspoof.ParseOptions(options.Spoof, options.SpoofMethod)
	if err != nil {
		return "", 0, err
	}
	if spoof == "" {
		return "", 0, nil
	}
	if options.DisableSNI || serverName == "" || M.ParseAddr(serverName).IsValid() {
		return "", 0, E.New("`spoof` requires TLS ClientHello with SNI")
	}
	if strings.EqualFold(spoof, serverName) {
		return "", 0, E.New("`spoof` must differ from `server_name`")
	}
	return spoof, method, nil
}

func applyTLSSpoof(conn net.Conn, spoof string, method tlsspoof.Method) (net.Conn, error) {
	if spoof == "" {
		return conn, nil
	}
	return tlsspoof.NewConn(conn, method, spoof)
}

func NewDialerFromOptions(ctx context.Context, logger logger.ContextLogger, dialer N.Dialer, serverAddress string, options option.OutboundTLSOptions) (N.Dialer, error) {
	if !options.Enabled {
		return dialer, nil
	}
	config, err := NewClientWithOptions(ClientOptions{
		Context:       ctx,
		Logger:        logger,
		ServerAddress: serverAddress,
		Options:       options,
	})
	if err != nil {
		return nil, err
	}
	return NewDialer(dialer, config), nil
}

func NewClient(ctx context.Context, logger logger.ContextLogger, serverAddress string, options option.OutboundTLSOptions) (Config, error) {
	return NewClientWithOptions(ClientOptions{
		Context:       ctx,
		Logger:        logger,
		ServerAddress: serverAddress,
		Options:       options,
	})
}

type ClientOptions struct {
	Context              context.Context
	Logger               logger.ContextLogger
	ServerAddress        string
	Options              option.OutboundTLSOptions
	AllowEmptyServerName bool
	KTLSCompatible       bool
}

func NewClientWithOptions(options ClientOptions) (Config, error) {
	if !options.Options.Enabled {
		return nil, nil
	}
	if !options.KTLSCompatible {
		if options.Options.KernelTx {
			options.Logger.Warn("enabling kTLS TX in current scenarios will definitely reduce performance, please checkout https://sing-box.sagernet.org/configuration/shared/tls/#kernel_tx")
		}
	}
	if options.Options.KernelRx {
		options.Logger.Warn("enabling kTLS RX will definitely reduce performance, please checkout https://sing-box.sagernet.org/configuration/shared/tls/#kernel_rx")
	}
	switch options.Options.Engine {
	case "", C.TLSEngineGo:
	case C.TLSEngineApple:
		return newAppleClient(options.Context, options.Logger, options.ServerAddress, options.Options, options.AllowEmptyServerName)
	case C.TLSEngineWindows:
		return newWindowsClient(options.Context, options.Logger, options.ServerAddress, options.Options, options.AllowEmptyServerName)
	default:
		return nil, E.New("unknown tls engine: ", options.Options.Engine)
	}
	if options.Options.Reality != nil && options.Options.Reality.Enabled {
		return newRealityClient(options.Context, options.Logger, options.ServerAddress, options.Options, options.AllowEmptyServerName)
	} else if options.Options.UTLS != nil && options.Options.UTLS.Enabled {
		return newUTLSClient(options.Context, options.Logger, options.ServerAddress, options.Options, options.AllowEmptyServerName)
	}
	return newSTDClient(options.Context, options.Logger, options.ServerAddress, options.Options, options.AllowEmptyServerName)
}

func ClientHandshake(ctx context.Context, conn net.Conn, config Config) (Conn, error) {
	tlsConn, err := aTLS.ClientHandshake(ctx, conn, config)
	if err != nil {
		return nil, wrapClientHandshakeError(err, conn, config)
	}
	readWaitConn, err := badtls.NewReadWaitConn(tlsConn)
	if err == nil {
		return readWaitConn, nil
	} else if err != os.ErrInvalid {
		return nil, err
	}
	return tlsConn, nil
}

type Dialer interface {
	N.Dialer
	DialTLSContext(ctx context.Context, destination M.Socksaddr) (Conn, error)
}

type defaultDialer struct {
	dialer N.Dialer
	config Config
}

func NewDialer(dialer N.Dialer, config Config) Dialer {
	return &defaultDialer{dialer, config}
}

func (d *defaultDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	if N.NetworkName(network) != N.NetworkTCP {
		return nil, os.ErrInvalid
	}
	return d.DialTLSContext(ctx, destination)
}

func (d *defaultDialer) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, os.ErrInvalid
}

func (d *defaultDialer) DialTLSContext(ctx context.Context, destination M.Socksaddr) (Conn, error) {
	return d.dialContext(ctx, destination, true)
}

func (d *defaultDialer) dialContext(ctx context.Context, destination M.Socksaddr, echRetry bool) (Conn, error) {
	conn, err := d.dialer.DialContext(ctx, N.NetworkTCP, destination)
	if err != nil {
		return nil, err
	}
	tlsConn, err := aTLS.ClientHandshake(ctx, conn, d.config)
	if err != nil {
		var echErr *tls.ECHRejectionError
		if echRetry && errors.As(err, &echErr) && len(echErr.RetryConfigList) > 0 {
			if echConfig, isECH := d.config.(ECHCapableConfig); isECH {
				conn.Close()
				echConfig.SetECHConfigList(echErr.RetryConfigList)
				return d.dialContext(ctx, destination, false)
			}
		}
		handshakeErr := wrapClientHandshakeError(err, conn, d.config)
		conn.Close()
		return nil, handshakeErr
	}
	return tlsConn, nil
}

type clientHandshakeDiagnostics struct {
	Engine       string
	ClientHello  string
	MinVersion   uint16
	MaxVersion   uint16
	DisableSNI   bool
	Capabilities []string
}

type clientHandshakeDiagnosticsProvider interface {
	clientHandshakeDiagnostics() clientHandshakeDiagnostics
}

func wrapClientHandshakeError(err error, conn net.Conn, config Config) error {
	return E.Cause(err, formatClientHandshakeContext(conn, config))
}

func formatClientHandshakeContext(conn net.Conn, config Config) string {
	peer := "unknown"
	if conn != nil && conn.RemoteAddr() != nil {
		peer = conn.RemoteAddr().String()
	}
	diagnostics := clientHandshakeDiagnostics{Engine: clientConfigType(config)}
	if provider, loaded := config.(clientHandshakeDiagnosticsProvider); loaded {
		diagnostics = provider.clientHandshakeDiagnostics()
		if diagnostics.Engine == "" {
			diagnostics.Engine = clientConfigType(config)
		}
	}
	serverName := ""
	var nextProtos []string
	if config != nil {
		serverName = config.ServerName()
		nextProtos = config.NextProtos()
	}
	sni := clientSNIStatus(serverName, diagnostics.DisableSNI)
	fields := []string{
		"server_name=" + quoteTLSDiagnosticValue(serverName),
		"sni=" + sni,
		"engine=" + quoteTLSDiagnosticValue(diagnostics.Engine),
		"client_hello=" + quoteTLSDiagnosticValue(diagnostics.ClientHello),
		"min_version=" + quoteTLSDiagnosticValue(formatTLSVersion(diagnostics.MinVersion)),
		"max_version=" + quoteTLSDiagnosticValue(formatTLSVersion(diagnostics.MaxVersion)),
		"alpn=" + quoteTLSDiagnosticValue(strings.Join(nextProtos, ",")),
	}
	if len(diagnostics.Capabilities) > 0 {
		fields = append(fields, "features="+quoteTLSDiagnosticValue(strings.Join(diagnostics.Capabilities, ",")))
	}
	return "TLS handshake with " + quoteTLSDiagnosticValue(peer) + " (" + strings.Join(fields, ", ") + ")"
}

func clientSNIStatus(serverName string, disabled bool) string {
	if disabled {
		return "disabled"
	}
	if serverName == "" {
		return "omitted"
	}
	ipName := strings.TrimSuffix(serverName, ".")
	ipName = strings.TrimPrefix(strings.TrimSuffix(ipName, "]"), "[")
	if zoneIndex := strings.LastIndexByte(ipName, '%'); zoneIndex >= 0 {
		ipName = ipName[:zoneIndex]
	}
	if net.ParseIP(ipName) != nil {
		return "omitted"
	}
	return "enabled"
}

func clientConfigType(config Config) string {
	if config == nil {
		return "unknown"
	}
	configType := reflect.TypeOf(config)
	for configType.Kind() == reflect.Pointer {
		configType = configType.Elem()
	}
	return configType.Name()
}

func formatTLSVersion(version uint16) string {
	switch version {
	case 0:
		return "default"
	case VersionTLS10:
		return "1.0"
	case VersionTLS11:
		return "1.1"
	case VersionTLS12:
		return "1.2"
	case VersionTLS13:
		return "1.3"
	default:
		return "0x" + strconv.FormatUint(uint64(version), 16)
	}
}

func quoteTLSDiagnosticValue(value string) string {
	// TLS names and ALPN values can originate from configuration. Keep failures
	// single-line and bounded so one malformed value cannot flood the log.
	const maxRunes = 256
	valueRunes := []rune(value)
	if len(valueRunes) > maxRunes {
		value = string(valueRunes[:maxRunes]) + "..."
	}
	return strconv.Quote(value)
}

func (d *defaultDialer) Upstream() any {
	return d.dialer
}

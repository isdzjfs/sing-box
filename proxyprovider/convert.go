package proxyprovider

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/json/badoption"
	N "github.com/sagernet/sing/common/network"
)

type unsupportedProxyTypeError struct {
	proxyType string
}

func (e unsupportedProxyTypeError) Error() string {
	if e.proxyType == "" {
		return "unsupported proxy type"
	}
	return "unsupported proxy type: " + e.proxyType
}

func convertProxy(provider option.ProxyProvider, proxy map[string]any, usedTags map[string]bool, domainResolver string) (option.Outbound, error) {
	rawName := stringValue(proxy, "name")
	tag := provider.Override.AdditionalPrefix + rawName
	proxyType := strings.ToLower(stringValue(proxy, "type"))
	var (
		outboundType    string
		outboundOptions any
		converter       func() (any, error)
		err             error
	)
	switch proxyType {
	case "ss", "shadowsocks":
		outboundType = C.TypeShadowsocks
		converter = func() (any, error) { return convertShadowsocks(provider, proxy, domainResolver) }
	case "vless":
		outboundType = C.TypeVLESS
		converter = func() (any, error) { return convertVLESS(provider, proxy, domainResolver) }
	case "vmess":
		outboundType = C.TypeVMess
		converter = func() (any, error) { return convertVMess(provider, proxy, domainResolver) }
	case "trojan":
		outboundType = C.TypeTrojan
		converter = func() (any, error) { return convertTrojan(provider, proxy, domainResolver) }
	case "hy2", "hysteria2":
		outboundType = C.TypeHysteria2
		converter = func() (any, error) { return convertHysteria2(provider, proxy, domainResolver) }
	case "anytls":
		outboundType = C.TypeAnyTLS
		converter = func() (any, error) { return convertAnyTLS(provider, proxy, domainResolver) }
	default:
		return option.Outbound{}, unsupportedProxyTypeError{proxyType: proxyType}
	}
	if usedTags[tag] {
		return option.Outbound{}, E.New("duplicate outbound tag: ", tag)
	}
	outboundOptions, err = converter()
	if err != nil {
		return option.Outbound{}, err
	}
	usedTags[tag] = true
	return option.Outbound{
		Type:    outboundType,
		Tag:     tag,
		Options: outboundOptions,
	}, nil
}

func convertShadowsocks(provider option.ProxyProvider, proxy map[string]any, domainResolver string) (*option.ShadowsocksOutboundOptions, error) {
	options := &option.ShadowsocksOutboundOptions{
		ServerOptions: serverOptions(proxy),
		Method:        stringValue(proxy, "cipher", "method"),
		Password:      stringValue(proxy, "password"),
		Network:       networkList(provider, proxy),
		Plugin:        stringValue(proxy, "plugin"),
	}
	if options.Method == "" {
		return nil, E.New("missing cipher")
	}
	if options.Password == "" {
		return nil, E.New("missing password")
	}
	if pluginOptions := pluginOptions(proxy); pluginOptions != "" {
		options.PluginOptions = pluginOptions
	}
	return options, applyDialerOverride(&options.DialerOptions, provider.Override, domainResolver)
}

func convertVLESS(provider option.ProxyProvider, proxy map[string]any, domainResolver string) (*option.VLESSOutboundOptions, error) {
	options := &option.VLESSOutboundOptions{
		ServerOptions: serverOptions(proxy),
		UUID:          stringValue(proxy, "uuid"),
		Flow:          stringValue(proxy, "flow"),
		Network:       networkList(provider, proxy),
		Transport:     v2rayTransportOptions(proxy),
	}
	options.TLS = tlsOptions(proxy, provider.Override, false)
	if options.UUID == "" {
		return nil, E.New("missing uuid")
	}
	if packetEncoding := stringValue(proxy, "packet-encoding", "packet_encoding"); packetEncoding != "" {
		options.PacketEncoding = &packetEncoding
	}
	return options, applyDialerOverride(&options.DialerOptions, provider.Override, domainResolver)
}

func convertVMess(provider option.ProxyProvider, proxy map[string]any, domainResolver string) (*option.VMessOutboundOptions, error) {
	options := &option.VMessOutboundOptions{
		ServerOptions: serverOptions(proxy),
		UUID:          stringValue(proxy, "uuid"),
		Security:      stringValue(proxy, "security", "cipher"),
		AlterId:       intValue(proxy, "alterId", "alter-id", "alter_id", "aid"),
		Network:       networkList(provider, proxy),
		Transport:     v2rayTransportOptions(proxy),
	}
	options.TLS = tlsOptions(proxy, provider.Override, false)
	if options.UUID == "" {
		return nil, E.New("missing uuid")
	}
	if options.Security == "" {
		options.Security = "auto"
	}
	if globalPadding, loaded := boolValue(proxy, "global-padding", "global_padding"); loaded {
		options.GlobalPadding = globalPadding
	}
	if authenticatedLength, loaded := boolValue(proxy, "authenticated-length", "authenticated_length"); loaded {
		options.AuthenticatedLength = authenticatedLength
	}
	if packetEncoding := stringValue(proxy, "packet-encoding", "packet_encoding"); packetEncoding != "" {
		options.PacketEncoding = packetEncoding
	}
	return options, applyDialerOverride(&options.DialerOptions, provider.Override, domainResolver)
}

func convertTrojan(provider option.ProxyProvider, proxy map[string]any, domainResolver string) (*option.TrojanOutboundOptions, error) {
	options := &option.TrojanOutboundOptions{
		ServerOptions: serverOptions(proxy),
		Password:      stringValue(proxy, "password"),
		Network:       networkList(provider, proxy),
		Transport:     v2rayTransportOptions(proxy),
	}
	options.TLS = tlsOptions(proxy, provider.Override, true)
	if options.Password == "" {
		return nil, E.New("missing password")
	}
	return options, applyDialerOverride(&options.DialerOptions, provider.Override, domainResolver)
}

func convertHysteria2(provider option.ProxyProvider, proxy map[string]any, domainResolver string) (*option.Hysteria2OutboundOptions, error) {
	options := &option.Hysteria2OutboundOptions{
		ServerOptions: serverOptions(proxy),
		ServerPorts:   hysteria2ServerPorts(proxy),
		Password:      stringValue(proxy, "password", "auth"),
		Network:       networkList(provider, proxy),
		UpMbps:        intValue(proxy, "up", "up-mbps", "up_mbps"),
		DownMbps:      intValue(proxy, "down", "down-mbps", "down_mbps"),
	}
	options.TLS = tlsOptions(proxy, provider.Override, true)
	if obfsType := stringValue(proxy, "obfs"); obfsType != "" {
		options.Obfs = &option.Hysteria2Obfs{
			Type:     obfsType,
			Password: stringValue(proxy, "obfs-password", "obfs_password"),
		}
	}
	return options, applyDialerOverride(&options.DialerOptions, provider.Override, domainResolver)
}

func hysteria2ServerPorts(proxy map[string]any) badoption.Listable[string] {
	values := stringsFromAny(firstValue(proxy, "ports", "mport", "server-ports", "server_ports"))
	if len(values) == 0 {
		return nil
	}
	for index, value := range values {
		values[index] = strings.ReplaceAll(value, "-", ":")
	}
	return badoption.Listable[string](values)
}

func convertAnyTLS(provider option.ProxyProvider, proxy map[string]any, domainResolver string) (*option.AnyTLSOutboundOptions, error) {
	options := &option.AnyTLSOutboundOptions{
		ServerOptions: serverOptions(proxy),
		Password:      stringValue(proxy, "password"),
	}
	options.TLS = tlsOptions(proxy, provider.Override, true)
	if options.Password == "" {
		return nil, E.New("missing password")
	}
	return options, applyDialerOverride(&options.DialerOptions, provider.Override, domainResolver)
}

func serverOptions(proxy map[string]any) option.ServerOptions {
	return option.ServerOptions{
		Server:     stringValue(proxy, "server"),
		ServerPort: uint16(intValue(proxy, "port", "server_port", "server-port")),
	}
}

func applyDialerOverride(dialerOptions *option.DialerOptions, override option.ProxyProviderOverride, domainResolver string) error {
	var strategy C.DomainStrategy
	switch strings.ToLower(strings.ReplaceAll(override.IPVersion, "-", "_")) {
	case "":
		return nil
	case "ipv4", "ipv4_only":
		strategy = C.DomainStrategyIPv4Only
	case "ipv6", "ipv6_only":
		strategy = C.DomainStrategyIPv6Only
	case "prefer_ipv4":
		strategy = C.DomainStrategyPreferIPv4
	case "prefer_ipv6":
		strategy = C.DomainStrategyPreferIPv6
	default:
		return E.New("unsupported override ip-version: ", override.IPVersion)
	}
	resolver := option.DomainResolveOptions{}
	if dialerOptions.DomainResolver != nil {
		resolver = *dialerOptions.DomainResolver
	}
	if resolver.Server == "" {
		resolver.Server = domainResolver
	}
	if resolver.Server == "" {
		return E.New("override ip-version requires route.default_domain_resolver")
	}
	resolver.Strategy = option.DomainStrategy(strategy)
	dialerOptions.DomainResolver = &resolver
	return nil
}

func networkList(provider option.ProxyProvider, proxy map[string]any) option.NetworkList {
	udp, hasUDP := boolValue(proxy, "udp")
	if provider.Override.UDP != nil {
		udp = *provider.Override.UDP
		hasUDP = true
	}
	if hasUDP && !udp {
		return option.NetworkList(N.NetworkTCP)
	}
	return ""
}

func tlsOptions(proxy map[string]any, override option.ProxyProviderOverride, defaultEnabled bool) *option.OutboundTLSOptions {
	enabled, hasTLS := boolValue(proxy, "tls")
	realityOptions := mapValue(proxy, "reality-opts", "reality_opts")
	if defaultEnabled || enabled || !hasTLS && hasTLSFields(proxy) || len(realityOptions) > 0 {
		tlsOptions := &option.OutboundTLSOptions{
			Enabled:    true,
			ServerName: stringValue(proxy, "sni", "servername", "server-name"),
			ALPN:       listableStringsFromAny(firstValue(proxy, "alpn")),
		}
		if insecure, loaded := boolValue(proxy, "skip-cert-verify", "skip_cert_verify", "insecure"); loaded {
			tlsOptions.Insecure = insecure
		}
		if override.Insecure != nil {
			tlsOptions.Insecure = *override.Insecure
		}
		if fingerprint := stringValue(proxy, "client-fingerprint", "client_fingerprint"); fingerprint != "" {
			tlsOptions.UTLS = &option.OutboundUTLSOptions{Enabled: true, Fingerprint: fingerprint}
		}
		if len(realityOptions) > 0 {
			tlsOptions.Reality = &option.OutboundRealityOptions{
				Enabled:   true,
				PublicKey: stringValue(realityOptions, "public-key", "public_key"),
				ShortID:   stringValue(realityOptions, "short-id", "short_id"),
			}
		}
		return tlsOptions
	}
	return nil
}

func hasTLSFields(proxy map[string]any) bool {
	return stringValue(proxy, "sni", "servername", "server-name", "client-fingerprint", "client_fingerprint") != "" ||
		len(listableStringsFromAny(firstValue(proxy, "alpn"))) > 0
}

func v2rayTransportOptions(proxy map[string]any) *option.V2RayTransportOptions {
	switch strings.ToLower(stringValue(proxy, "network")) {
	case "ws", "websocket":
		wsOptions := mapValue(proxy, "ws-opts", "ws_opts")
		return &option.V2RayTransportOptions{
			Type: C.V2RayTransportTypeWebsocket,
			WebsocketOptions: option.V2RayWebsocketOptions{
				Path:                stringValue(wsOptions, "path"),
				Headers:             headerFromAny(firstValue(wsOptions, "headers")),
				MaxEarlyData:        uint32(intValue(wsOptions, "max-early-data", "max_early_data")),
				EarlyDataHeaderName: stringValue(wsOptions, "early-data-header-name", "early_data_header_name"),
			},
		}
	case "grpc":
		grpcOptions := mapValue(proxy, "grpc-opts", "grpc_opts")
		return &option.V2RayTransportOptions{
			Type: C.V2RayTransportTypeGRPC,
			GRPCOptions: option.V2RayGRPCOptions{
				ServiceName: stringValue(grpcOptions, "grpc-service-name", "serviceName", "service_name"),
			},
		}
	case "http", "h2":
		httpOptions := mapValue(proxy, "h2-opts", "h2_opts", "http-opts", "http_opts")
		return &option.V2RayTransportOptions{
			Type: C.V2RayTransportTypeHTTP,
			HTTPOptions: option.V2RayHTTPOptions{
				Host:    listableStringsFromAny(firstValue(httpOptions, "host")),
				Path:    stringValue(httpOptions, "path"),
				Headers: headerFromAny(firstValue(httpOptions, "headers")),
			},
		}
	default:
		return nil
	}
}

func pluginOptions(proxy map[string]any) string {
	if value := stringValue(proxy, "plugin-opts", "plugin_opts"); value != "" {
		return value
	}
	pluginOptions := mapValue(proxy, "plugin-opts", "plugin_opts")
	if len(pluginOptions) == 0 {
		return ""
	}
	keys := make([]string, 0, len(pluginOptions))
	for key := range pluginOptions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	values := make([]string, 0, len(keys))
	for _, key := range keys {
		values = append(values, fmt.Sprintf("%s=%s", key, valueToString(pluginOptions[key])))
	}
	return strings.Join(values, ";")
}

func stringValue(values map[string]any, keys ...string) string {
	for _, key := range keys {
		value, loaded := values[key]
		if !loaded {
			continue
		}
		switch typedValue := value.(type) {
		case string:
			return typedValue
		case fmt.Stringer:
			return typedValue.String()
		case int:
			return strconv.Itoa(typedValue)
		case int64:
			return strconv.FormatInt(typedValue, 10)
		case uint64:
			return strconv.FormatUint(typedValue, 10)
		case float64:
			if typedValue == float64(int64(typedValue)) {
				return strconv.FormatInt(int64(typedValue), 10)
			}
		}
	}
	return ""
}

func intValue(values map[string]any, keys ...string) int {
	for _, key := range keys {
		value, loaded := values[key]
		if !loaded {
			continue
		}
		switch typedValue := value.(type) {
		case int:
			return typedValue
		case int64:
			return int(typedValue)
		case uint64:
			return int(typedValue)
		case float64:
			return int(typedValue)
		case string:
			parsed, err := strconv.Atoi(typedValue)
			if err == nil {
				return parsed
			}
		}
	}
	return 0
}

func boolValue(values map[string]any, keys ...string) (bool, bool) {
	for _, key := range keys {
		value, loaded := values[key]
		if !loaded {
			continue
		}
		switch typedValue := value.(type) {
		case bool:
			return typedValue, true
		case string:
			parsed, err := strconv.ParseBool(typedValue)
			if err == nil {
				return parsed, true
			}
		}
	}
	return false, false
}

func mapValue(values map[string]any, keys ...string) map[string]any {
	value := firstValue(values, keys...)
	switch typedValue := value.(type) {
	case map[string]any:
		return typedValue
	case map[any]any:
		result := make(map[string]any, len(typedValue))
		for key, value := range typedValue {
			result[valueToString(key)] = value
		}
		return result
	default:
		return nil
	}
}

func firstValue(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, loaded := values[key]; loaded {
			return value
		}
	}
	return nil
}

func listableStringsFromAny(value any) badoption.Listable[string] {
	values := stringsFromAny(value)
	if len(values) == 0 {
		return nil
	}
	return badoption.Listable[string](values)
}

func stringsFromAny(value any) []string {
	switch typedValue := value.(type) {
	case nil:
		return nil
	case string:
		if typedValue == "" {
			return nil
		}
		return []string{typedValue}
	case []string:
		return typedValue
	case []any:
		values := make([]string, 0, len(typedValue))
		for _, item := range typedValue {
			if value := valueToString(item); value != "" {
				values = append(values, value)
			}
		}
		return values
	default:
		if value := valueToString(value); value != "" {
			return []string{value}
		}
		return nil
	}
}

func headerFromAny(value any) badoption.HTTPHeader {
	headers := mapFromAny(value)
	if len(headers) == 0 {
		return nil
	}
	result := make(badoption.HTTPHeader, len(headers))
	for key, value := range headers {
		values := stringsFromAny(value)
		if len(values) > 0 {
			result[key] = badoption.Listable[string](values)
		}
	}
	return result
}

func mapFromAny(value any) map[string]any {
	switch typedValue := value.(type) {
	case map[string]any:
		return typedValue
	case map[any]any:
		result := make(map[string]any, len(typedValue))
		for key, value := range typedValue {
			result[valueToString(key)] = value
		}
		return result
	default:
		return nil
	}
}

func valueToString(value any) string {
	switch typedValue := value.(type) {
	case string:
		return typedValue
	case fmt.Stringer:
		return typedValue.String()
	case int:
		return strconv.Itoa(typedValue)
	case int64:
		return strconv.FormatInt(typedValue, 10)
	case uint64:
		return strconv.FormatUint(typedValue, 10)
	case float64:
		if typedValue == float64(int64(typedValue)) {
			return strconv.FormatInt(int64(typedValue), 10)
		}
		return strconv.FormatFloat(typedValue, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(typedValue)
	default:
		return ""
	}
}

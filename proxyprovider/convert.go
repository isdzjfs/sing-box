package proxyprovider

import (
	"encoding/base64"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/byteformats"
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

type convertedProxy struct {
	tag       string
	proxyType string
	outbound  *option.Outbound
	endpoint  *option.Endpoint
}

func convertProxy(provider option.ProxyProvider, proxy map[string]any, usedTags map[string]bool, domainResolver string) (convertedProxy, error) {
	rawName := stringValue(proxy, "name")
	tag := provider.Override.AdditionalPrefix + rawName
	proxyType := strings.ToLower(stringValue(proxy, "type"))
	var (
		outboundType    string
		endpointType    string
		outboundOptions any
		converter       func() (any, error)
		err             error
	)
	switch proxyType {
	case "http", "https":
		outboundType = C.TypeHTTP
		converter = func() (any, error) { return convertHTTP(provider, proxy, domainResolver, proxyType == "https") }
	case "socks", "socks5", "socks5h":
		outboundType = C.TypeSOCKS
		converter = func() (any, error) { return convertSOCKS(provider, proxy, domainResolver) }
	case "ss", "shadowsocks":
		outboundType = C.TypeShadowsocks
		converter = func() (any, error) { return convertShadowsocks(provider, proxy, domainResolver) }
	case "snell":
		outboundType = C.TypeSnell
		converter = func() (any, error) { return convertSnell(provider, proxy, domainResolver) }
	case "vless":
		outboundType = C.TypeVLESS
		converter = func() (any, error) { return convertVLESS(provider, proxy, domainResolver) }
	case "vmess":
		outboundType = C.TypeVMess
		converter = func() (any, error) { return convertVMess(provider, proxy, domainResolver) }
	case "trojan":
		outboundType = C.TypeTrojan
		converter = func() (any, error) { return convertTrojan(provider, proxy, domainResolver) }
	case "hysteria":
		outboundType = C.TypeHysteria
		converter = func() (any, error) { return convertHysteria(provider, proxy, domainResolver) }
	case "hy2", "hysteria2":
		outboundType = C.TypeHysteria2
		converter = func() (any, error) { return convertHysteria2(provider, proxy, domainResolver) }
	case "tuic":
		outboundType = C.TypeTUIC
		converter = func() (any, error) { return convertTUIC(provider, proxy, domainResolver) }
	case "wireguard":
		endpointType = C.TypeWireGuard
		converter = func() (any, error) { return convertWireGuard(provider, proxy, domainResolver) }
	case "ssh":
		outboundType = C.TypeSSH
		converter = func() (any, error) { return convertSSH(provider, proxy, domainResolver) }
	case "anytls":
		outboundType = C.TypeAnyTLS
		converter = func() (any, error) { return convertAnyTLS(provider, proxy, domainResolver) }
	default:
		return convertedProxy{}, unsupportedProxyTypeError{proxyType: proxyType}
	}
	if usedTags[tag] {
		return convertedProxy{}, E.New("duplicate outbound tag: ", tag)
	}
	outboundOptions, err = converter()
	if err != nil {
		return convertedProxy{}, err
	}
	usedTags[tag] = true
	converted := convertedProxy{
		tag:       tag,
		proxyType: stringValue(proxy, "type"),
	}
	if endpointType != "" {
		endpoint := option.Endpoint{
			Type:    endpointType,
			Tag:     tag,
			Options: outboundOptions,
		}
		converted.endpoint = &endpoint
		return converted, nil
	}
	outbound := option.Outbound{
		Type:    outboundType,
		Tag:     tag,
		Options: outboundOptions,
	}
	converted.outbound = &outbound
	return converted, nil
}

func convertHTTP(provider option.ProxyProvider, proxy map[string]any, domainResolver string, defaultTLS bool) (*option.HTTPOutboundOptions, error) {
	options := &option.HTTPOutboundOptions{
		ServerOptions: serverOptions(proxy),
		Username:      stringValue(proxy, "username", "user"),
		Password:      stringValue(proxy, "password", "pass"),
		Headers:       headerFromAny(firstValue(proxy, "headers")),
	}
	options.TLS = tlsOptions(proxy, provider.Override, defaultTLS)
	if options.TLS != nil && options.TLS.UTLS != nil {
		options.TLS.UTLS = nil
	}
	if path := stringValue(proxy, "path"); path != "" {
		options.Path = path
	}
	return options, applyDialerOverride(&options.DialerOptions, provider.Override, domainResolver)
}

func convertSOCKS(provider option.ProxyProvider, proxy map[string]any, domainResolver string) (*option.SOCKSOutboundOptions, error) {
	if enabled, loaded := boolValue(proxy, "tls"); loaded && enabled {
		return nil, unsupportedProxyTypeError{proxyType: "socks5 tls"}
	}
	options := &option.SOCKSOutboundOptions{
		ServerOptions: serverOptions(proxy),
		Version:       "5",
		Username:      stringValue(proxy, "username", "user"),
		Password:      stringValue(proxy, "password", "pass"),
		Network:       networkList(provider, proxy),
	}
	return options, applyDialerOverride(&options.DialerOptions, provider.Override, domainResolver)
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

func convertSnell(provider option.ProxyProvider, proxy map[string]any, domainResolver string) (*option.SnellOutboundOptions, error) {
	version := intValue(proxy, "version")
	if version == 0 || version == 5 {
		version = 4
	}
	if version != 4 && version != 6 {
		return nil, unsupportedProxyTypeError{proxyType: fmt.Sprintf("snell v%d", version)}
	}
	options := &option.SnellOutboundOptions{
		ServerOptions: serverOptions(proxy),
		Version:       version,
		PSK:           stringValue(proxy, "psk"),
		Reuse:         boolValueDefault(proxy, "reuse"),
		Network:       snellNetworkList(provider, proxy),
	}
	if userKey := stringValue(proxy, "userkey", "user-key", "user_key"); userKey != "" {
		options.UserKey = userKey
	}
	obfsOptions := mapValue(proxy, "obfs-opts", "obfs_opts")
	options.ObfsOptions.ObfsMode = stringValue(obfsOptions, "mode")
	options.ObfsOptions.ObfsHost = stringValue(obfsOptions, "host")
	options.V6Options.Mode = stringValue(proxy, "mode")
	if options.PSK == "" {
		return nil, E.New("missing psk")
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

func convertHysteria(provider option.ProxyProvider, proxy map[string]any, domainResolver string) (*option.HysteriaOutboundOptions, error) {
	switch protocol := strings.ToLower(stringValue(proxy, "protocol")); protocol {
	case "", "udp":
	default:
		return nil, unsupportedProxyTypeError{proxyType: "hysteria " + protocol}
	}
	options := &option.HysteriaOutboundOptions{
		ServerOptions:       serverOptions(proxy),
		ServerPorts:         hysteria2ServerPorts(proxy),
		UpMbps:              intValue(proxy, "up-mbps", "up_mbps", "up-speed", "up_speed"),
		DownMbps:            intValue(proxy, "down-mbps", "down_mbps", "down-speed", "down_speed"),
		Obfs:                stringValue(proxy, "obfs"),
		AuthString:          stringValue(proxy, "auth-str", "auth_str"),
		Network:             networkList(provider, proxy),
		ReceiveWindowConn:   uint64(intValue(proxy, "recv-window-conn", "recv_window_conn")),
		ReceiveWindow:       uint64(intValue(proxy, "recv-window", "recv_window")),
		DisableMTUDiscovery: boolValueDefault(proxy, "disable-mtu-discovery", "disable_mtu_discovery"),
	}
	var err error
	options.Up, err = networkBytesFromAny(firstValue(proxy, "up"))
	if err != nil {
		return nil, E.Cause(err, "up")
	}
	options.Down, err = networkBytesFromAny(firstValue(proxy, "down"))
	if err != nil {
		return nil, E.Cause(err, "down")
	}
	if auth := stringValue(proxy, "auth"); auth != "" {
		options.Auth, err = base64.StdEncoding.DecodeString(auth)
		if err != nil {
			return nil, E.Cause(err, "auth")
		}
	}
	if hopInterval := intValue(proxy, "hop-interval", "hop_interval"); hopInterval > 0 {
		options.HopInterval = badoption.Duration(time.Duration(hopInterval) * time.Second)
	}
	if receiveWindow := intValue(proxy, "recv-window-conn", "recv_window_conn"); receiveWindow > 0 {
		streamReceiveWindow, windowErr := memoryBytesFromInt(receiveWindow)
		if windowErr != nil {
			return nil, E.Cause(windowErr, "recv-window-conn")
		}
		options.StreamReceiveWindow = streamReceiveWindow
	}
	if receiveWindow := intValue(proxy, "recv-window", "recv_window"); receiveWindow > 0 {
		connectionReceiveWindow, windowErr := memoryBytesFromInt(receiveWindow)
		if windowErr != nil {
			return nil, E.Cause(windowErr, "recv-window")
		}
		options.ConnectionReceiveWindow = connectionReceiveWindow
	}
	if disableMTUDiscovery, loaded := boolValue(proxy, "disable-mtu-discovery", "disable_mtu_discovery"); loaded {
		options.DisablePathMTUDiscovery = disableMTUDiscovery
	}
	options.TLS = tlsOptions(proxy, provider.Override, true)
	if options.TLS != nil && len(options.TLS.ALPN) == 0 {
		options.TLS.ALPN = badoption.Listable[string]{"hysteria"}
	}
	if options.AuthString == "" && len(options.Auth) == 0 {
		return nil, E.New("missing auth-str")
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

func convertTUIC(provider option.ProxyProvider, proxy map[string]any, domainResolver string) (*option.TUICOutboundOptions, error) {
	if token := stringValue(proxy, "token"); token != "" {
		return nil, unsupportedProxyTypeError{proxyType: "tuic v4/token"}
	}
	options := &option.TUICOutboundOptions{
		ServerOptions:     serverOptions(proxy),
		UUID:              stringValue(proxy, "uuid"),
		Password:          stringValue(proxy, "password"),
		CongestionControl: stringValue(proxy, "congestion-controller", "congestion_control"),
		UDPRelayMode:      stringValue(proxy, "udp-relay-mode", "udp_relay_mode"),
		Network:           networkList(provider, proxy),
	}
	options.TLS = tlsOptions(proxy, provider.Override, true)
	if options.TLS != nil {
		// TUIC is QUIC-based in sing-box; uTLS is only valid for TCP TLS handshakes.
		options.TLS.UTLS = nil
	}
	if options.UUID == "" {
		return nil, E.New("missing uuid")
	}
	if options.Password == "" {
		return nil, E.New("missing password")
	}
	if serverIP := stringValue(proxy, "ip"); serverIP != "" {
		if options.TLS.ServerName == "" && !options.TLS.DisableSNI {
			options.TLS.ServerName = options.Server
		}
		options.Server = serverIP
	}
	if udpOverStream, loaded := boolValue(proxy, "udp-over-stream", "udp_over_stream"); loaded {
		options.UDPOverStream = udpOverStream
		if udpOverStream {
			options.UDPRelayMode = ""
		}
	}
	if tcpFastOpen, loaded := boolValue(proxy, "tfo", "fast-open", "fast_open"); loaded {
		options.TCPFastOpen = tcpFastOpen
	}
	if zeroRTT, loaded := boolValue(proxy, "zero-rtt-handshake", "zero_rtt_handshake", "reduce-rtt", "reduce_rtt"); loaded {
		options.ZeroRTTHandshake = zeroRTT
	}
	if heartbeat := intValue(proxy, "heartbeat-interval", "heartbeat_interval"); heartbeat > 0 {
		options.Heartbeat = badoption.Duration(time.Duration(heartbeat) * time.Millisecond)
	}
	if receiveWindow := intValue(proxy, "recv-window-conn", "recv_window_conn"); receiveWindow > 0 {
		streamReceiveWindow, err := memoryBytesFromInt(receiveWindow)
		if err != nil {
			return nil, E.Cause(err, "recv-window-conn")
		}
		options.StreamReceiveWindow = streamReceiveWindow
	}
	if receiveWindow := intValue(proxy, "recv-window", "recv_window"); receiveWindow > 0 {
		connectionReceiveWindow, err := memoryBytesFromInt(receiveWindow)
		if err != nil {
			return nil, E.Cause(err, "recv-window")
		}
		options.ConnectionReceiveWindow = connectionReceiveWindow
	}
	if maxOpenStreams := intValue(proxy, "max-open-streams", "max_open_streams"); maxOpenStreams > 0 {
		options.MaxConcurrentStreams = maxOpenStreams
	}
	if disableMTUDiscovery, loaded := boolValue(proxy, "disable-mtu-discovery", "disable_mtu_discovery"); loaded {
		options.DisablePathMTUDiscovery = disableMTUDiscovery
	}
	return options, applyDialerOverride(&options.DialerOptions, provider.Override, domainResolver)
}

func convertWireGuard(provider option.ProxyProvider, proxy map[string]any, domainResolver string) (*option.WireGuardEndpointOptions, error) {
	options := &option.WireGuardEndpointOptions{
		System:     boolValueDefault(proxy, "system", "system-interface", "system_interface"),
		Name:       stringValue(proxy, "interface-name", "interface_name"),
		MTU:        uint32(intValue(proxy, "mtu")),
		Address:    wireGuardAddresses(proxy),
		PrivateKey: stringValue(proxy, "private-key", "private_key"),
		ListenPort: uint16(intValue(proxy, "listen-port", "listen_port")),
		Workers:    intValue(proxy, "workers"),
	}
	peers, err := wireGuardPeers(proxy, options.Address)
	if err != nil {
		return nil, err
	}
	options.Peers = peers
	if options.PrivateKey == "" {
		return nil, E.New("missing private-key")
	}
	if len(options.Address) == 0 {
		return nil, E.New("missing local address")
	}
	if len(options.Peers) == 0 {
		return nil, E.New("missing peer")
	}
	return options, applyDialerOverride(&options.DialerOptions, provider.Override, domainResolver)
}

func convertSSH(provider option.ProxyProvider, proxy map[string]any, domainResolver string) (*option.SSHOutboundOptions, error) {
	options := &option.SSHOutboundOptions{
		ServerOptions:        serverOptions(proxy),
		User:                 stringValue(proxy, "username", "user"),
		Password:             stringValue(proxy, "password"),
		PrivateKey:           listableStringsFromAny(firstValue(proxy, "private-key", "private_key")),
		PrivateKeyPath:       stringValue(proxy, "private-key-path", "private_key_path"),
		PrivateKeyPassphrase: stringValue(proxy, "private-key-passphrase", "private_key_passphrase"),
		HostKey:              listableStringsFromAny(firstValue(proxy, "host-key", "host_key")),
		HostKeyAlgorithms:    listableStringsFromAny(firstValue(proxy, "host-key-algorithms", "host_key_algorithms")),
		ClientVersion:        stringValue(proxy, "client-version", "client_version"),
		Cipher:               listableStringsFromAny(firstValue(proxy, "cipher")),
		MAC:                  listableStringsFromAny(firstValue(proxy, "mac")),
		KexAlgorithm:         listableStringsFromAny(firstValue(proxy, "kex-algorithm", "kex_algorithm")),
	}
	if options.User == "" {
		return nil, E.New("missing username")
	}
	if options.Password == "" && len(options.PrivateKey) == 0 && options.PrivateKeyPath == "" {
		return nil, E.New("missing ssh authentication")
	}
	return options, applyDialerOverride(&options.DialerOptions, provider.Override, domainResolver)
}

func memoryBytesFromInt(value int) (byteformats.MemoryBytes, error) {
	var result byteformats.MemoryBytes
	err := result.UnmarshalJSON([]byte(strconv.Quote(strconv.Itoa(value) + " B")))
	return result, err
}

func networkBytesFromAny(value any) (*byteformats.NetworkBytesCompat, error) {
	if value == nil {
		return nil, nil
	}
	rawValue := strings.TrimSpace(valueToString(value))
	if rawValue == "" {
		return nil, nil
	}
	if _, err := strconv.ParseFloat(rawValue, 64); err == nil {
		rawValue += " Mbps"
	}
	var result byteformats.NetworkBytesCompat
	err := result.UnmarshalJSON([]byte(strconv.Quote(rawValue)))
	if err != nil {
		return nil, err
	}
	return &result, nil
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

func wireGuardAddresses(proxy map[string]any) badoption.Listable[netip.Prefix] {
	var addresses []netip.Prefix
	addresses = append(addresses, wireGuardPrefixValues(firstValue(proxy, "address", "addresses", "local-address", "local_address"))...)
	if ip := stringValue(proxy, "ip"); ip != "" {
		addresses = append(addresses, wireGuardPrefixValue(ip, 32))
	}
	if ipv6 := stringValue(proxy, "ipv6"); ipv6 != "" {
		addresses = append(addresses, wireGuardPrefixValue(ipv6, 128))
	}
	addresses = filterValidPrefixes(addresses)
	if len(addresses) == 0 {
		return nil
	}
	return badoption.Listable[netip.Prefix](addresses)
}

func wireGuardPeers(proxy map[string]any, localAddresses []netip.Prefix) ([]option.WireGuardPeer, error) {
	defaultReserved, err := wireGuardReserved(firstValue(proxy, "reserved"))
	if err != nil {
		return nil, E.Cause(err, "reserved")
	}
	rawPeers := mapsFromAny(firstValue(proxy, "peers"))
	if len(rawPeers) == 0 {
		peer := option.WireGuardPeer{
			Address:                     stringValue(proxy, "server"),
			Port:                        uint16(intValue(proxy, "port", "server_port", "server-port")),
			PublicKey:                   stringValue(proxy, "public-key", "public_key"),
			PreSharedKey:                stringValue(proxy, "pre-shared-key", "pre_shared_key"),
			AllowedIPs:                  wireGuardAllowedIPs(firstValue(proxy, "allowed-ips", "allowed_ips"), localAddresses),
			PersistentKeepaliveInterval: uint16(intValue(proxy, "persistent-keepalive", "persistent_keepalive")),
			Reserved:                    defaultReserved,
		}
		if peer.PublicKey == "" {
			return nil, E.New("missing public-key")
		}
		return []option.WireGuardPeer{peer}, nil
	}
	peers := make([]option.WireGuardPeer, 0, len(rawPeers))
	for index, rawPeer := range rawPeers {
		reserved, reservedErr := wireGuardReserved(firstValue(rawPeer, "reserved"))
		if reservedErr != nil {
			return nil, E.Cause(reservedErr, "peer ", index, " reserved")
		}
		if len(reserved) == 0 {
			reserved = defaultReserved
		}
		peer := option.WireGuardPeer{
			Address:                     stringValue(rawPeer, "server", "address"),
			Port:                        uint16(intValue(rawPeer, "port", "server_port", "server-port")),
			PublicKey:                   stringValue(rawPeer, "public-key", "public_key"),
			PreSharedKey:                stringValue(rawPeer, "pre-shared-key", "pre_shared_key"),
			AllowedIPs:                  wireGuardAllowedIPs(firstValue(rawPeer, "allowed-ips", "allowed_ips"), nil),
			PersistentKeepaliveInterval: uint16(intValue(rawPeer, "persistent-keepalive", "persistent_keepalive")),
			Reserved:                    reserved,
		}
		if peer.PublicKey == "" {
			return nil, E.New("missing public-key for peer ", index)
		}
		if len(peer.AllowedIPs) == 0 {
			return nil, E.New("missing allowed-ips for peer ", index)
		}
		peers = append(peers, peer)
	}
	return peers, nil
}

func wireGuardAllowedIPs(value any, localAddresses []netip.Prefix) badoption.Listable[netip.Prefix] {
	allowedIPs := wireGuardPrefixValues(value)
	if len(allowedIPs) == 0 && len(localAddresses) > 0 {
		for _, address := range localAddresses {
			if address.Addr().Is4() {
				allowedIPs = append(allowedIPs, netip.MustParsePrefix("0.0.0.0/0"))
			} else if address.Addr().Is6() {
				allowedIPs = append(allowedIPs, netip.MustParsePrefix("::/0"))
			}
		}
	}
	allowedIPs = filterValidPrefixes(allowedIPs)
	if len(allowedIPs) == 0 {
		return nil
	}
	return badoption.Listable[netip.Prefix](allowedIPs)
}

func wireGuardPrefixValues(value any) []netip.Prefix {
	values := stringsFromAny(value)
	prefixes := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		prefixes = append(prefixes, wireGuardPrefixValue(value, 0))
	}
	return prefixes
}

func wireGuardPrefixValue(value string, defaultBits int) netip.Prefix {
	if !strings.Contains(value, "/") {
		addr, err := netip.ParseAddr(value)
		if err != nil {
			return netip.Prefix{}
		}
		if defaultBits == 0 {
			defaultBits = addr.BitLen()
		}
		return netip.PrefixFrom(addr, defaultBits)
	}
	prefix, err := netip.ParsePrefix(value)
	if err != nil {
		return netip.Prefix{}
	}
	return prefix
}

func filterValidPrefixes(prefixes []netip.Prefix) []netip.Prefix {
	valid := prefixes[:0]
	for _, prefix := range prefixes {
		if prefix.IsValid() {
			valid = append(valid, prefix)
		}
	}
	return valid
}

func wireGuardReserved(value any) ([]uint8, error) {
	switch typedValue := value.(type) {
	case nil:
		return nil, nil
	case []uint8:
		return typedValue, nil
	case []int:
		result := make([]uint8, 0, len(typedValue))
		for _, item := range typedValue {
			if item < 0 || item > 255 {
				return nil, E.New("invalid byte value: ", item)
			}
			result = append(result, uint8(item))
		}
		return result, nil
	case []any:
		result := make([]uint8, 0, len(typedValue))
		for _, item := range typedValue {
			value := intValue(map[string]any{"value": item}, "value")
			if value < 0 || value > 255 {
				return nil, E.New("invalid byte value: ", item)
			}
			result = append(result, uint8(value))
		}
		return result, nil
	case string:
		if typedValue == "" {
			return nil, nil
		}
		if decoded, err := base64.StdEncoding.DecodeString(typedValue); err == nil {
			return decoded, nil
		}
		parts := strings.Split(typedValue, ",")
		result := make([]uint8, 0, len(parts))
		for _, part := range parts {
			value, err := strconv.Atoi(strings.TrimSpace(part))
			if err != nil {
				return nil, err
			}
			if value < 0 || value > 255 {
				return nil, E.New("invalid byte value: ", value)
			}
			result = append(result, uint8(value))
		}
		return result, nil
	default:
		return nil, E.New("unsupported reserved value")
	}
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

func snellNetworkList(provider option.ProxyProvider, proxy map[string]any) option.NetworkList {
	udp, hasUDP := boolValue(proxy, "udp")
	if provider.Override.UDP != nil {
		udp = *provider.Override.UDP
		hasUDP = true
	}
	if !hasUDP || !udp {
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
		if disableSNI, loaded := boolValue(proxy, "disable-sni", "disable_sni"); loaded {
			tlsOptions.DisableSNI = disableSNI
		}
		if fingerprint := stringValue(proxy, "client-fingerprint", "client_fingerprint", "fp"); fingerprint != "" {
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

func boolValueDefault(values map[string]any, keys ...string) bool {
	value, _ := boolValue(values, keys...)
	return value
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

func mapsFromAny(value any) []map[string]any {
	switch typedValue := value.(type) {
	case nil:
		return nil
	case []map[string]any:
		return typedValue
	case []any:
		values := make([]map[string]any, 0, len(typedValue))
		for _, item := range typedValue {
			if mapped := mapFromAny(item); len(mapped) > 0 {
				values = append(values, mapped)
			}
		}
		return values
	default:
		if mapped := mapFromAny(value); len(mapped) > 0 {
			return []map[string]any{mapped}
		}
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

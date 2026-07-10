package proxyprovider

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
)

func TestExpandFileProviderSelectorUseAndOverrides(t *testing.T) {
	subscriptionPath := writeSubscription(t, `
proxies:
  - name: 流量 1GB
    type: ss
    server: example.com
    port: 8388
    cipher: 2022-blake3-aes-128-gcm
    password: filtered
  - name: HK VLESS
    type: vless
    server: vless.example.com
    port: 443
    uuid: 00000000-0000-0000-0000-000000000000
    udp: false
    tls: true
    skip-cert-verify: true
    sni: vless.example.com
    network: ws
    ws-opts:
      path: /ws
      headers:
        Host: cdn.example.com
`)
	enableUDP := true
	disableInsecure := false
	selectorOptions := &option.SelectorOutboundOptions{Use: []string{"sub"}}
	urlTestOptions := &option.URLTestOutboundOptions{Use: []string{"sub"}}
	options := option.Options{
		ProxyProviderDefaults: &option.ProxyProvider{
			ExcludeFilter: "流量|到期",
			Override: option.ProxyProviderOverride{
				UDP:       &enableUDP,
				IPVersion: "ipv4",
				Insecure:  &disableInsecure,
			},
		},
		ProxyProviders: map[string]option.ProxyProvider{
			"sub": {
				Type: "file",
				Path: subscriptionPath,
				Override: option.ProxyProviderOverride{
					AdditionalPrefix: "良心云 | ",
				},
			},
		},
		Outbounds: []option.Outbound{
			{Type: C.TypeSelector, Tag: "proxy", Options: selectorOptions},
			{Type: C.TypeURLTest, Tag: "auto", Options: urlTestOptions},
		},
		Route: &option.RouteOptions{
			DefaultDomainResolver: &option.DomainResolveOptions{Server: "bootstrap"},
		},
	}

	if err := Expand(context.Background(), log.NewNOPFactory().Logger(), &options); err != nil {
		t.Fatal(err)
	}
	if got, want := selectorOptions.Outbounds, []string{"良心云 | HK VLESS"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("selector outbounds = %#v, want %#v", got, want)
	}
	if got, want := urlTestOptions.Outbounds, []string{"良心云 | HK VLESS"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("urltest outbounds = %#v, want %#v", got, want)
	}
	if len(options.Outbounds) != 3 {
		t.Fatalf("outbound count = %d, want 3", len(options.Outbounds))
	}
	generated := options.Outbounds[2]
	if generated.Type != C.TypeVLESS || generated.Tag != "良心云 | HK VLESS" {
		t.Fatalf("generated outbound = %s/%s", generated.Type, generated.Tag)
	}
	vlessOptions := generated.Options.(*option.VLESSOutboundOptions)
	if vlessOptions.Network != "" {
		t.Fatalf("network = %q, want default tcp+udp after override", vlessOptions.Network)
	}
	if vlessOptions.DomainStrategy != option.DomainStrategy(C.DomainStrategyAsIS) {
		t.Fatalf("domain strategy = %v, want unset", vlessOptions.DomainStrategy)
	}
	if vlessOptions.DomainResolver == nil {
		t.Fatal("domain resolver is nil")
	}
	if vlessOptions.DomainResolver.Server != "bootstrap" {
		t.Fatalf("domain resolver server = %q, want bootstrap", vlessOptions.DomainResolver.Server)
	}
	if vlessOptions.DomainResolver.Strategy != option.DomainStrategy(C.DomainStrategyIPv4Only) {
		t.Fatalf("domain resolver strategy = %v, want ipv4 only", vlessOptions.DomainResolver.Strategy)
	}
	if vlessOptions.TLS == nil || !vlessOptions.TLS.Enabled || vlessOptions.TLS.ServerName != "vless.example.com" {
		t.Fatalf("unexpected TLS options: %#v", vlessOptions.TLS)
	}
	if !vlessOptions.TLS.Insecure {
		t.Fatalf("tls insecure = false, want file provider to keep node setting")
	}
	if vlessOptions.Transport == nil || vlessOptions.Transport.Type != C.V2RayTransportTypeWebsocket {
		t.Fatalf("unexpected transport: %#v", vlessOptions.Transport)
	}
	if vlessOptions.Transport.WebsocketOptions.Path != "/ws" {
		t.Fatalf("ws path = %q", vlessOptions.Transport.WebsocketOptions.Path)
	}
}

func TestExpandHTTPProviderDefaultsInsecure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`
proxies:
  - name: HK VLESS
    type: vless
    server: vless.example.com
    port: 443
    uuid: 00000000-0000-0000-0000-000000000000
    tls: true
    skip-cert-verify: true
`))
	}))
	defer server.Close()
	disableInsecure := false
	selectorOptions := &option.SelectorOutboundOptions{Use: []string{"sub"}}
	options := option.Options{
		ProxyProviderDefaults: &option.ProxyProvider{
			Type: "http",
			Override: option.ProxyProviderOverride{
				Insecure: &disableInsecure,
			},
		},
		ProxyProviders: map[string]option.ProxyProvider{
			"sub": {
				URL:  server.URL,
				Path: filepath.Join(t.TempDir(), "subscription.yaml"),
			},
		},
		Outbounds: []option.Outbound{
			{Type: C.TypeSelector, Tag: "proxy", Options: selectorOptions},
		},
	}

	if err := Expand(context.Background(), log.NewNOPFactory().Logger(), &options); err != nil {
		t.Fatal(err)
	}
	if got, want := selectorOptions.Outbounds, []string{"HK VLESS"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("selector outbounds = %#v, want %#v", got, want)
	}
	if len(options.Outbounds) != 2 {
		t.Fatalf("outbound count = %d, want 2", len(options.Outbounds))
	}
	vlessOptions := options.Outbounds[1].Options.(*option.VLESSOutboundOptions)
	if vlessOptions.TLS == nil || vlessOptions.TLS.Insecure {
		t.Fatalf("unexpected TLS options: %#v", vlessOptions.TLS)
	}
}

func TestMergeProxyProviderOverrideKeepsExplicitInsecureFalse(t *testing.T) {
	defaultInsecure := true
	overrideInsecure := false
	merged := mergeProxyProviderOverride(option.ProxyProviderOverride{
		Insecure: &defaultInsecure,
	}, option.ProxyProviderOverride{
		Insecure: &overrideInsecure,
	})
	if merged.Insecure == nil || *merged.Insecure {
		t.Fatalf("merged insecure = %#v, want explicit false", merged.Insecure)
	}
}

func TestExpandConvertsRequestedProxyTypes(t *testing.T) {
	subscriptionPath := writeSubscription(t, `
proxies:
  - name: AnyTLS
    type: anytls
    server: any.example.com
    port: 443
    password: any-pass
    sni: any.example.com
  - name: VLESS
    type: vless
    server: vless.example.com
    port: 443
    uuid: 00000000-0000-0000-0000-000000000001
    tls: true
  - name: Trojan
    type: trojan
    server: trojan.example.com
    port: 443
    password: trojan-pass
  - name: HY2
    type: hy2
    server: hy2.example.com
    port: 443
    ports: 60000-65530
    password: hy2-pass
    obfs: salamander
    obfs-password: obfs-pass
  - name: SS
    type: ss
    server: ss.example.com
    port: 8388
    cipher: aes-128-gcm
    password: ss-pass
  - name: SSR
    type: ssr
    server: ssr.example.com
    port: 8388
    cipher: aes-256-cfb
    password: ssr-pass
    obfs: tls1.2_ticket_auth
    obfs-param: cdn.example.com
    protocol: auth_chain_a
    protocol-param: "123:ssr-proto-pass"
  - name: VMess
    type: vmess
    server: vmess.example.com
    port: 443
    uuid: 00000000-0000-0000-0000-000000000003
    cipher: auto
`)
	selectorOptions := &option.SelectorOutboundOptions{Use: []string{"sub"}}
	options := option.Options{
		ProxyProviders: map[string]option.ProxyProvider{
			"sub": {
				Type: "file",
				Path: subscriptionPath,
			},
		},
		Outbounds: []option.Outbound{
			{Type: C.TypeSelector, Tag: "proxy", Options: selectorOptions},
		},
	}

	if err := Expand(context.Background(), log.NewNOPFactory().Logger(), &options); err != nil {
		t.Fatal(err)
	}
	wantTypes := map[string]string{
		"AnyTLS": C.TypeAnyTLS,
		"VLESS":  C.TypeVLESS,
		"Trojan": C.TypeTrojan,
		"HY2":    C.TypeHysteria2,
		"SS":     C.TypeShadowsocks,
		"SSR":    C.TypeShadowsocksR,
		"VMess":  C.TypeVMess,
	}
	if len(selectorOptions.Outbounds) != len(wantTypes) {
		t.Fatalf("selector outbounds = %#v", selectorOptions.Outbounds)
	}
	for _, outbound := range options.Outbounds[1:] {
		if wantType := wantTypes[outbound.Tag]; outbound.Type != wantType {
			t.Fatalf("outbound %s type = %s, want %s", outbound.Tag, outbound.Type, wantType)
		}
	}
	hy2Options := options.Outbounds[4].Options.(*option.Hysteria2OutboundOptions)
	if hy2Options.Obfs == nil || hy2Options.Obfs.Password != "obfs-pass" {
		t.Fatalf("unexpected hy2 obfs: %#v", hy2Options.Obfs)
	}
	if len(hy2Options.ServerPorts) != 1 || hy2Options.ServerPorts[0] != "60000:65530" {
		t.Fatalf("server ports = %#v, want 60000:65530", hy2Options.ServerPorts)
	}
	ssrOptions := options.Outbounds[6].Options.(*option.ShadowsocksROutboundOptions)
	if ssrOptions.Method != "aes-256-cfb" || ssrOptions.Obfs != "tls1.2_ticket_auth" || ssrOptions.Protocol != "auth_chain_a" {
		t.Fatalf("unexpected ssr options: %#v", ssrOptions)
	}
	if ssrOptions.ObfsParam != "cdn.example.com" || ssrOptions.ProtocolParam != "123:ssr-proto-pass" {
		t.Fatalf("unexpected ssr params: %#v", ssrOptions)
	}
}

func TestExpandConvertsAdditionalProxyTypes(t *testing.T) {
	subscriptionPath := writeSubscription(t, `
proxies:
  - name: HTTP
    type: http
    server: http.example.com
    port: 443
    username: http-user
    password: http-pass
    tls: true
    skip-cert-verify: true
    sni: proxy.example.com
    headers:
      X-Test: provider
  - name: SOCKS
    type: socks5
    server: socks.example.com
    port: 1080
    username: socks-user
    password: socks-pass
    udp: false
  - name: Snell
    type: snell
    server: snell.example.com
    port: 44046
    psk: snell-psk
    version: 5
    reuse: true
    obfs-opts:
      mode: http
      host: bing.com
  - name: Snell UDP
    type: snell
    server: snell-udp.example.com
    port: 44046
    psk: snell-udp-psk
    version: 4
    udp: true
  - name: Hysteria
    type: hysteria
    server: hy.example.com
    port: 443
    ports: 1000-2000
    auth-str: hy-pass
    up: 30 Mbps
    down: 200 Mbps
    hop-interval: 15
    skip-cert-verify: true
  - name: WireGuard
    type: wireguard
    server: 162.159.192.1
    port: 2480
    ip: 172.16.0.2
    ipv6: fd01:5ca1:ab1e:80fa:ab85:6eea:213f:f4a5
    public-key: Cr8hWlKvtDt7nrvf+f0brNQQzabAqrjfBvas9pmowjo=
    private-key: eCtXsJZ27+4PbhDkHnB923tkUn2Gj59wZw5wFA75MnU=
    reserved: U4An
    persistent-keepalive: 25
  - name: SSH
    type: ssh
    server: ssh.example.com
    port: 22
    username: root
    password: ssh-pass
`)
	selectorOptions := &option.SelectorOutboundOptions{Use: []string{"sub"}}
	options := option.Options{
		ProxyProviders: map[string]option.ProxyProvider{
			"sub": {
				Type: "file",
				Path: subscriptionPath,
			},
		},
		Outbounds: []option.Outbound{
			{Type: C.TypeSelector, Tag: "proxy", Options: selectorOptions},
		},
	}

	if err := Expand(context.Background(), log.NewNOPFactory().Logger(), &options); err != nil {
		t.Fatal(err)
	}
	wantOutbounds := []string{"HTTP", "SOCKS", "Snell", "Snell UDP", "Hysteria", "WireGuard", "SSH"}
	if got := selectorOptions.Outbounds; len(got) != len(wantOutbounds) {
		t.Fatalf("selector outbounds = %#v, want %#v", got, wantOutbounds)
	} else {
		for index, want := range wantOutbounds {
			if got[index] != want {
				t.Fatalf("selector outbounds = %#v, want %#v", got, wantOutbounds)
			}
		}
	}
	if len(options.Endpoints) != 1 {
		t.Fatalf("endpoint count = %d, want 1", len(options.Endpoints))
	}
	if options.Endpoints[0].Type != C.TypeWireGuard || options.Endpoints[0].Tag != "WireGuard" {
		t.Fatalf("generated endpoint = %s/%s", options.Endpoints[0].Type, options.Endpoints[0].Tag)
	}
	wireGuardOptions := options.Endpoints[0].Options.(*option.WireGuardEndpointOptions)
	if len(wireGuardOptions.Address) != 2 || len(wireGuardOptions.Peers) != 1 {
		t.Fatalf("unexpected wireguard options: %#v", wireGuardOptions)
	}
	if len(wireGuardOptions.Peers[0].AllowedIPs) != 2 || len(wireGuardOptions.Peers[0].Reserved) != 3 {
		t.Fatalf("unexpected wireguard peer: %#v", wireGuardOptions.Peers[0])
	}
	generatedByTag := make(map[string]option.Outbound)
	for _, outbound := range options.Outbounds[1:] {
		generatedByTag[outbound.Tag] = outbound
	}
	if generatedByTag["HTTP"].Type != C.TypeHTTP {
		t.Fatalf("HTTP type = %s", generatedByTag["HTTP"].Type)
	}
	httpOptions := generatedByTag["HTTP"].Options.(*option.HTTPOutboundOptions)
	if httpOptions.Username != "http-user" || httpOptions.TLS == nil || !httpOptions.TLS.Insecure || httpOptions.TLS.ServerName != "proxy.example.com" {
		t.Fatalf("unexpected http options: %#v", httpOptions)
	}
	if got := httpOptions.Headers["X-Test"]; len(got) != 1 || got[0] != "provider" {
		t.Fatalf("http header = %#v, want provider", got)
	}
	socksOptions := generatedByTag["SOCKS"].Options.(*option.SOCKSOutboundOptions)
	if socksOptions.Network != option.NetworkList("tcp") || socksOptions.Username != "socks-user" {
		t.Fatalf("unexpected socks options: %#v", socksOptions)
	}
	snellOptions := generatedByTag["Snell"].Options.(*option.SnellOutboundOptions)
	if snellOptions.Version != 4 || snellOptions.ObfsOptions.ObfsMode != "http" || !snellOptions.Reuse || snellOptions.Network != option.NetworkList("tcp") {
		t.Fatalf("unexpected snell options: %#v", snellOptions)
	}
	snellUDPOptions := generatedByTag["Snell UDP"].Options.(*option.SnellOutboundOptions)
	if snellUDPOptions.Network != "" {
		t.Fatalf("snell udp network = %q, want default tcp+udp", snellUDPOptions.Network)
	}
	hysteriaOptions := generatedByTag["Hysteria"].Options.(*option.HysteriaOutboundOptions)
	if hysteriaOptions.AuthString != "hy-pass" || hysteriaOptions.Up.Value() == 0 || hysteriaOptions.Down.Value() == 0 {
		t.Fatalf("unexpected hysteria auth/bandwidth: %#v", hysteriaOptions)
	}
	if len(hysteriaOptions.ServerPorts) != 1 || hysteriaOptions.ServerPorts[0] != "1000:2000" {
		t.Fatalf("hysteria server ports = %#v, want 1000:2000", hysteriaOptions.ServerPorts)
	}
	if hysteriaOptions.TLS == nil || len(hysteriaOptions.TLS.ALPN) != 1 || hysteriaOptions.TLS.ALPN[0] != "hysteria" {
		t.Fatalf("unexpected hysteria tls: %#v", hysteriaOptions.TLS)
	}
	sshOptions := generatedByTag["SSH"].Options.(*option.SSHOutboundOptions)
	if sshOptions.User != "root" || sshOptions.Password != "ssh-pass" {
		t.Fatalf("unexpected ssh options: %#v", sshOptions)
	}
}

func TestExpandConvertsXHTTPTransports(t *testing.T) {
	subscriptionPath := writeSubscription(t, `
proxies:
  - name: XHTTP VLESS
    type: vless
    server: xhttp.example.com
    port: 443
    uuid: 00000000-0000-0000-0000-000000000004
    udp: true
    tls: true
    servername: www.cloudflare.com
    client-fingerprint: chrome
    reality-opts:
      public-key: public-key
      short-id: short-id
    network: xhttp
    xhttp-opts:
      host: cdn.example.com
      path: /xhttp
      mode: stream-up
      headers:
        X-Test: provider
      x-padding-bytes: 100-200
  - name: SplitHTTP Trojan
    type: trojan
    server: split.example.com
    port: 443
    password: split-pass
    network: splithttp
    splithttp-opts:
      path: /split
`)
	selectorOptions := &option.SelectorOutboundOptions{Use: []string{"sub"}}
	options := option.Options{
		ProxyProviders: map[string]option.ProxyProvider{
			"sub": {
				Type: "file",
				Path: subscriptionPath,
			},
		},
		Outbounds: []option.Outbound{
			{Type: C.TypeSelector, Tag: "proxy", Options: selectorOptions},
		},
	}

	if err := Expand(context.Background(), log.NewNOPFactory().Logger(), &options); err != nil {
		t.Fatal(err)
	}
	if got, want := selectorOptions.Outbounds, []string{"XHTTP VLESS", "SplitHTTP Trojan"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("selector outbounds = %#v, want %#v", got, want)
	}
	vlessOptions := options.Outbounds[1].Options.(*option.VLESSOutboundOptions)
	if vlessOptions.Transport == nil || vlessOptions.Transport.Type != C.V2RayTransportTypeXHTTP {
		t.Fatalf("vless transport = %#v, want xhttp", vlessOptions.Transport)
	}
	xhttpOptions := vlessOptions.Transport.XHTTPOptions
	if xhttpOptions.Host != "cdn.example.com" || xhttpOptions.Path != "/xhttp" || xhttpOptions.Mode != "stream-up" {
		t.Fatalf("unexpected xhttp options: %#v", xhttpOptions)
	}
	if vlessOptions.Network != "" {
		t.Fatalf("vless network = %q, want default tcp+udp", vlessOptions.Network)
	}
	if vlessOptions.TLS == nil || vlessOptions.TLS.ServerName != "www.cloudflare.com" {
		t.Fatalf("unexpected vless tls: %#v", vlessOptions.TLS)
	}
	if vlessOptions.TLS.UTLS == nil || vlessOptions.TLS.UTLS.Fingerprint != "chrome" {
		t.Fatalf("unexpected vless utls: %#v", vlessOptions.TLS.UTLS)
	}
	if vlessOptions.TLS.Reality == nil || vlessOptions.TLS.Reality.PublicKey != "public-key" || vlessOptions.TLS.Reality.ShortID != "short-id" {
		t.Fatalf("unexpected vless reality: %#v", vlessOptions.TLS.Reality)
	}
	if got := xhttpOptions.Headers["X-Test"]; len(got) != 1 || got[0] != "provider" {
		t.Fatalf("xhttp header = %#v, want provider", got)
	}
	if xhttpOptions.XPaddingBytes == nil || xhttpOptions.XPaddingBytes.From != 100 || xhttpOptions.XPaddingBytes.To != 200 {
		t.Fatalf("xhttp padding = %#v, want 100-200", xhttpOptions.XPaddingBytes)
	}
	trojanOptions := options.Outbounds[2].Options.(*option.TrojanOutboundOptions)
	if trojanOptions.Transport == nil || trojanOptions.Transport.Type != C.V2RayTransportTypeSplitHTTP {
		t.Fatalf("trojan transport = %#v, want splithttp", trojanOptions.Transport)
	}
	if trojanOptions.Transport.XHTTPOptions.Path != "/split" {
		t.Fatalf("splithttp path = %q, want /split", trojanOptions.Transport.XHTTPOptions.Path)
	}
}

func TestExpandConvertsTUICOptions(t *testing.T) {
	subscriptionPath := writeSubscription(t, `
proxies:
  - name: TUIC V5
    type: tuic
    server: tuic.example.com
    port: 443
    uuid: 00000000-0000-0000-0000-000000000004
    password: tuic-pass
    alpn: [h3]
    skip-cert-verify: true
    disable-sni: true
    client-fingerprint: firefox
    reduce-rtt: true
    udp-relay-mode: quic
    congestion-controller: bbr
    heartbeat-interval: 12000
    recv-window-conn: 1234
    recv-window: 5678
    max-open-streams: 20
    disable-mtu-discovery: true
  - name: TUIC IP
    type: tuic
    server: tuic-domain.example.com
    ip: 203.0.113.10
    port: 443
    uuid: 00000000-0000-0000-0000-000000000005
    password: tuic-ip-pass
`)
	selectorOptions := &option.SelectorOutboundOptions{Use: []string{"sub"}}
	options := option.Options{
		ProxyProviders: map[string]option.ProxyProvider{
			"sub": {
				Type: "file",
				Path: subscriptionPath,
			},
		},
		Outbounds: []option.Outbound{
			{Type: C.TypeSelector, Tag: "proxy", Options: selectorOptions},
		},
	}

	if err := Expand(context.Background(), log.NewNOPFactory().Logger(), &options); err != nil {
		t.Fatal(err)
	}
	if got, want := selectorOptions.Outbounds, []string{"TUIC V5", "TUIC IP"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("selector outbounds = %#v, want %#v", got, want)
	}
	generated := options.Outbounds[1]
	if generated.Type != C.TypeTUIC || generated.Tag != "TUIC V5" {
		t.Fatalf("generated outbound = %s/%s", generated.Type, generated.Tag)
	}
	tuicOptions := generated.Options.(*option.TUICOutboundOptions)
	if tuicOptions.UUID != "00000000-0000-0000-0000-000000000004" || tuicOptions.Password != "tuic-pass" {
		t.Fatalf("tuic credentials = %s/%s", tuicOptions.UUID, tuicOptions.Password)
	}
	if tuicOptions.CongestionControl != "bbr" || tuicOptions.UDPRelayMode != "quic" {
		t.Fatalf("tuic transport options = congestion:%q udp_relay_mode:%q", tuicOptions.CongestionControl, tuicOptions.UDPRelayMode)
	}
	if !tuicOptions.ZeroRTTHandshake || time.Duration(tuicOptions.Heartbeat) != 12*time.Second {
		t.Fatalf("tuic timing options = zero_rtt:%v heartbeat:%v", tuicOptions.ZeroRTTHandshake, tuicOptions.Heartbeat)
	}
	if tuicOptions.StreamReceiveWindow.Value() != 1234 || tuicOptions.ConnectionReceiveWindow.Value() != 5678 {
		t.Fatalf("tuic receive windows = stream:%d connection:%d", tuicOptions.StreamReceiveWindow.Value(), tuicOptions.ConnectionReceiveWindow.Value())
	}
	if tuicOptions.MaxConcurrentStreams != 20 || !tuicOptions.DisablePathMTUDiscovery {
		t.Fatalf("tuic quic options = max_streams:%d disable_pmtu:%v", tuicOptions.MaxConcurrentStreams, tuicOptions.DisablePathMTUDiscovery)
	}
	if tuicOptions.TLS == nil || !tuicOptions.TLS.Enabled || !tuicOptions.TLS.Insecure || !tuicOptions.TLS.DisableSNI {
		t.Fatalf("unexpected tuic TLS options: %#v", tuicOptions.TLS)
	}
	if tuicOptions.TLS.UTLS != nil {
		t.Fatalf("tuic uTLS = %#v, want nil", tuicOptions.TLS.UTLS)
	}
	if len(tuicOptions.TLS.ALPN) != 1 || tuicOptions.TLS.ALPN[0] != "h3" {
		t.Fatalf("tuic alpn = %#v, want h3", tuicOptions.TLS.ALPN)
	}
	ipOptions := options.Outbounds[2].Options.(*option.TUICOutboundOptions)
	if ipOptions.Server != "203.0.113.10" || ipOptions.TLS == nil || ipOptions.TLS.ServerName != "tuic-domain.example.com" {
		t.Fatalf("tuic ip override = server:%q tls:%#v", ipOptions.Server, ipOptions.TLS)
	}
}

func TestExpandConvertsTUICUDPOverStreamClearsRelayMode(t *testing.T) {
	subscriptionPath := writeSubscription(t, `
proxies:
  - name: TUIC UoS
    type: tuic
    server: tuic.example.com
    port: 443
    uuid: 00000000-0000-0000-0000-000000000006
    password: tuic-pass
    udp-relay-mode: quic
    udp-over-stream: true
`)
	options := option.Options{
		ProxyProviders: map[string]option.ProxyProvider{
			"sub": {
				Type: "file",
				Path: subscriptionPath,
			},
		},
		Outbounds: []option.Outbound{
			{Type: C.TypeSelector, Tag: "proxy", Options: &option.SelectorOutboundOptions{Use: []string{"sub"}}},
		},
	}

	if err := Expand(context.Background(), log.NewNOPFactory().Logger(), &options); err != nil {
		t.Fatal(err)
	}
	tuicOptions := options.Outbounds[1].Options.(*option.TUICOutboundOptions)
	if !tuicOptions.UDPOverStream || tuicOptions.UDPRelayMode != "" {
		t.Fatalf("tuic udp options = udp_over_stream:%v udp_relay_mode:%q", tuicOptions.UDPOverStream, tuicOptions.UDPRelayMode)
	}
}

func TestExpandConvertsBase64TUICURIList(t *testing.T) {
	subscriptionPath := writeSubscription(t, base64.StdEncoding.EncodeToString([]byte(`
REMARKS=example
tuic://00000000-0000-0000-0000-000000000007:tuic-uri-pass@tuic.example.com:443?security=tls&fp=firefox&sni=tuic-sni.example.com&alpn=h3&congestion_control=bbr&udp_relay_mode=quic&reduce_rtt=1&udp=1&tfo=1#HK+Gomami+01
`)))
	selectorOptions := &option.SelectorOutboundOptions{Use: []string{"sub"}}
	options := option.Options{
		ProxyProviders: map[string]option.ProxyProvider{
			"sub": {
				Type: "file",
				Path: subscriptionPath,
			},
		},
		Outbounds: []option.Outbound{
			{Type: C.TypeSelector, Tag: "proxy", Options: selectorOptions},
		},
	}

	if err := Expand(context.Background(), log.NewNOPFactory().Logger(), &options); err != nil {
		t.Fatal(err)
	}
	if got, want := selectorOptions.Outbounds, []string{"HK Gomami 01"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("selector outbounds = %#v, want %#v", got, want)
	}
	generated := options.Outbounds[1]
	if generated.Type != C.TypeTUIC || generated.Tag != "HK Gomami 01" {
		t.Fatalf("generated outbound = %s/%s", generated.Type, generated.Tag)
	}
	tuicOptions := generated.Options.(*option.TUICOutboundOptions)
	if tuicOptions.UUID != "00000000-0000-0000-0000-000000000007" || tuicOptions.Password != "tuic-uri-pass" {
		t.Fatalf("tuic credentials = %s/%s", tuicOptions.UUID, tuicOptions.Password)
	}
	if tuicOptions.Server != "tuic.example.com" || tuicOptions.ServerPort != 443 {
		t.Fatalf("tuic server = %s:%d", tuicOptions.Server, tuicOptions.ServerPort)
	}
	if tuicOptions.CongestionControl != "bbr" || tuicOptions.UDPRelayMode != "quic" {
		t.Fatalf("tuic transport options = congestion:%q udp_relay_mode:%q", tuicOptions.CongestionControl, tuicOptions.UDPRelayMode)
	}
	if !tuicOptions.ZeroRTTHandshake || !tuicOptions.TCPFastOpen {
		t.Fatalf("tuic bool options = zero_rtt:%v tfo:%v", tuicOptions.ZeroRTTHandshake, tuicOptions.TCPFastOpen)
	}
	if tuicOptions.TLS == nil || tuicOptions.TLS.ServerName != "tuic-sni.example.com" {
		t.Fatalf("unexpected tuic TLS options: %#v", tuicOptions.TLS)
	}
	if tuicOptions.TLS.UTLS != nil {
		t.Fatalf("tuic uTLS = %#v, want nil", tuicOptions.TLS.UTLS)
	}
	if len(tuicOptions.TLS.ALPN) != 1 || tuicOptions.TLS.ALPN[0] != "h3" {
		t.Fatalf("tuic alpn = %#v, want h3", tuicOptions.TLS.ALPN)
	}
}

func TestExpandConvertsBase64SSRURIList(t *testing.T) {
	encodeSSR := func(value string) string {
		return base64.RawURLEncoding.EncodeToString([]byte(value))
	}
	ssrBody := "ssr.example.com:8388:auth_chain_a:aes-256-cfb:tls1.2_ticket_auth:" +
		encodeSSR("ssr-pass") +
		"/?obfsparam=" + encodeSSR("cdn.example.com") +
		"&protoparam=" + encodeSSR("123:ssr-proto-pass") +
		"&remarks=" + encodeSSR("HK SSR 01")
	subscriptionPath := writeSubscription(t, base64.StdEncoding.EncodeToString([]byte(`
REMARKS=example
ssr://`+encodeSSR(ssrBody)+`
`)))
	selectorOptions := &option.SelectorOutboundOptions{Use: []string{"sub"}}
	options := option.Options{
		ProxyProviders: map[string]option.ProxyProvider{
			"sub": {
				Type: "file",
				Path: subscriptionPath,
			},
		},
		Outbounds: []option.Outbound{
			{Type: C.TypeSelector, Tag: "proxy", Options: selectorOptions},
		},
	}

	if err := Expand(context.Background(), log.NewNOPFactory().Logger(), &options); err != nil {
		t.Fatal(err)
	}
	if got, want := selectorOptions.Outbounds, []string{"HK SSR 01"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("selector outbounds = %#v, want %#v", got, want)
	}
	generated := options.Outbounds[1]
	if generated.Type != C.TypeShadowsocksR || generated.Tag != "HK SSR 01" {
		t.Fatalf("generated outbound = %s/%s", generated.Type, generated.Tag)
	}
	ssrOptions := generated.Options.(*option.ShadowsocksROutboundOptions)
	if ssrOptions.Server != "ssr.example.com" || ssrOptions.ServerPort != 8388 {
		t.Fatalf("ssr server = %s:%d", ssrOptions.Server, ssrOptions.ServerPort)
	}
	if ssrOptions.Method != "aes-256-cfb" || ssrOptions.Password != "ssr-pass" {
		t.Fatalf("ssr credentials = method:%q password:%q", ssrOptions.Method, ssrOptions.Password)
	}
	if ssrOptions.Obfs != "tls1.2_ticket_auth" || ssrOptions.ObfsParam != "cdn.example.com" {
		t.Fatalf("ssr obfs = obfs:%q param:%q", ssrOptions.Obfs, ssrOptions.ObfsParam)
	}
	if ssrOptions.Protocol != "auth_chain_a" || ssrOptions.ProtocolParam != "123:ssr-proto-pass" {
		t.Fatalf("ssr protocol = protocol:%q param:%q", ssrOptions.Protocol, ssrOptions.ProtocolParam)
	}
	if ssrOptions.Network != "" {
		t.Fatalf("ssr network = %q, want default tcp+udp", ssrOptions.Network)
	}
}

func TestExpandConvertsVMessOptions(t *testing.T) {
	subscriptionPath := writeSubscription(t, `
proxies:
  - name: JP VMess WS
    type: vmess
    server: vmess.example.com
    port: 443
    uuid: 00000000-0000-0000-0000-000000000003
    alterId: 1
    cipher: chacha20-poly1305
    udp: false
    tls: true
    servername: vmess.example.com
    network: ws
    ws-opts:
      path: /ws
      headers:
        Host: cdn.example.com
    packet-encoding: xudp
    global-padding: true
    authenticated-length: true
`)
	selectorOptions := &option.SelectorOutboundOptions{Use: []string{"sub"}}
	options := option.Options{
		ProxyProviders: map[string]option.ProxyProvider{
			"sub": {
				Type: "file",
				Path: subscriptionPath,
			},
		},
		Outbounds: []option.Outbound{
			{Type: C.TypeSelector, Tag: "proxy", Options: selectorOptions},
		},
	}

	if err := Expand(context.Background(), log.NewNOPFactory().Logger(), &options); err != nil {
		t.Fatal(err)
	}
	if got, want := selectorOptions.Outbounds, []string{"JP VMess WS"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("selector outbounds = %#v, want %#v", got, want)
	}
	generated := options.Outbounds[1]
	if generated.Type != C.TypeVMess || generated.Tag != "JP VMess WS" {
		t.Fatalf("generated outbound = %s/%s", generated.Type, generated.Tag)
	}
	vmessOptions := generated.Options.(*option.VMessOutboundOptions)
	if vmessOptions.UUID != "00000000-0000-0000-0000-000000000003" {
		t.Fatalf("uuid = %q", vmessOptions.UUID)
	}
	if vmessOptions.Security != "chacha20-poly1305" {
		t.Fatalf("security = %q", vmessOptions.Security)
	}
	if vmessOptions.AlterId != 1 {
		t.Fatalf("alter id = %d, want 1", vmessOptions.AlterId)
	}
	if vmessOptions.Network != option.NetworkList("tcp") {
		t.Fatalf("network = %q, want tcp", vmessOptions.Network)
	}
	if vmessOptions.PacketEncoding != "xudp" {
		t.Fatalf("packet encoding = %q, want xudp", vmessOptions.PacketEncoding)
	}
	if !vmessOptions.GlobalPadding || !vmessOptions.AuthenticatedLength {
		t.Fatalf("vmess protocol options = global_padding:%v authenticated_length:%v", vmessOptions.GlobalPadding, vmessOptions.AuthenticatedLength)
	}
	if vmessOptions.TLS == nil || !vmessOptions.TLS.Enabled || vmessOptions.TLS.ServerName != "vmess.example.com" {
		t.Fatalf("unexpected TLS options: %#v", vmessOptions.TLS)
	}
	if vmessOptions.Transport == nil || vmessOptions.Transport.Type != C.V2RayTransportTypeWebsocket {
		t.Fatalf("unexpected transport: %#v", vmessOptions.Transport)
	}
	if vmessOptions.Transport.WebsocketOptions.Path != "/ws" {
		t.Fatalf("ws path = %q", vmessOptions.Transport.WebsocketOptions.Path)
	}
	if got := vmessOptions.Transport.WebsocketOptions.Headers["Host"]; len(got) != 1 || got[0] != "cdn.example.com" {
		t.Fatalf("ws host header = %#v, want cdn.example.com", got)
	}
}

func TestExpandSkipsUnsupportedProxyTypes(t *testing.T) {
	subscriptionPath := writeSubscription(t, `
proxies:
  - name: Shared
    type: tuic
    server: tuic.example.com
    port: 443
    token: tuic-token
  - name: Shared
    type: ss
    server: ss.example.com
    port: 8388
    cipher: aes-128-gcm
    password: ss-pass
  - name: Shared
    type: mieru
    server: mieru.example.com
    port: 443
`)
	selectorOptions := &option.SelectorOutboundOptions{Use: []string{"sub"}}
	options := option.Options{
		ProxyProviders: map[string]option.ProxyProvider{
			"sub": {
				Type: "file",
				Path: subscriptionPath,
			},
		},
		Outbounds: []option.Outbound{
			{Type: C.TypeSelector, Tag: "proxy", Options: selectorOptions},
		},
	}

	if err := Expand(context.Background(), log.NewNOPFactory().Logger(), &options); err != nil {
		t.Fatal(err)
	}
	if got, want := selectorOptions.Outbounds, []string{"Shared"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("selector outbounds = %#v, want %#v", got, want)
	}
	if len(options.Outbounds) != 2 {
		t.Fatalf("outbound count = %d, want 2", len(options.Outbounds))
	}
	if generated := options.Outbounds[1]; generated.Type != C.TypeShadowsocks || generated.Tag != "Shared" {
		t.Fatalf("generated outbound = %s/%s", generated.Type, generated.Tag)
	}
}

func TestExpandUnsupportedOnlyProviderRemainsFatal(t *testing.T) {
	subscriptionPath := writeSubscription(t, `
proxies:
  - name: TUIC
    type: tuic
    server: tuic.example.com
    port: 443
    token: tuic-token
`)
	options := option.Options{
		ProxyProviders: map[string]option.ProxyProvider{
			"sub": {
				Type: "file",
				Path: subscriptionPath,
			},
		},
		Outbounds: []option.Outbound{
			{Type: C.TypeSelector, Tag: "proxy", Options: &option.SelectorOutboundOptions{Use: []string{"sub"}}},
		},
	}

	if err := Expand(context.Background(), log.NewNOPFactory().Logger(), &options); err == nil {
		t.Fatal("expected provider without usable proxies to fail")
	}
}

func TestExpandDoesNotTreatCertificateFingerprintAsUTLS(t *testing.T) {
	subscriptionPath := writeSubscription(t, `
proxies:
  - name: HY2
    type: hysteria2
    server: hy2.example.com
    port: 443
    password: hy2-pass
    fingerprint: 65b3acd7db555768304a16abb6f4366c1a0c0bb5cec81429617f0150d7d66726
  - name: VLESS
    type: vless
    server: vless.example.com
    port: 443
    uuid: 00000000-0000-0000-0000-000000000001
    tls: true
    client-fingerprint: chrome
`)
	options := option.Options{
		ProxyProviders: map[string]option.ProxyProvider{
			"sub": {
				Type: "file",
				Path: subscriptionPath,
			},
		},
		Outbounds: []option.Outbound{
			{Type: C.TypeSelector, Tag: "proxy", Options: &option.SelectorOutboundOptions{Use: []string{"sub"}}},
		},
	}

	if err := Expand(context.Background(), log.NewNOPFactory().Logger(), &options); err != nil {
		t.Fatal(err)
	}

	hy2Options := options.Outbounds[1].Options.(*option.Hysteria2OutboundOptions)
	if hy2Options.TLS == nil || !hy2Options.TLS.Enabled {
		t.Fatalf("unexpected hy2 TLS options: %#v", hy2Options.TLS)
	}
	if hy2Options.TLS.UTLS != nil {
		t.Fatalf("hy2 certificate fingerprint was converted to uTLS: %#v", hy2Options.TLS.UTLS)
	}

	vlessOptions := options.Outbounds[2].Options.(*option.VLESSOutboundOptions)
	if vlessOptions.TLS == nil || vlessOptions.TLS.UTLS == nil || vlessOptions.TLS.UTLS.Fingerprint != "chrome" {
		t.Fatalf("unexpected vless uTLS options: %#v", vlessOptions.TLS)
	}
}

func TestExpandProviderExcludeType(t *testing.T) {
	subscriptionPath := writeSubscription(t, `
proxies:
  - name: HY2
    type: hysteria2
    server: hy2.example.com
    port: 443
    password: hy2-pass
  - name: SS
    type: ss
    server: ss.example.com
    port: 8388
    cipher: aes-128-gcm
    password: ss-pass
`)
	selectorOptions := &option.SelectorOutboundOptions{Use: []string{"sub"}}
	options := option.Options{
		ProxyProviders: map[string]option.ProxyProvider{
			"sub": {
				Type:        "file",
				Path:        subscriptionPath,
				ExcludeType: "Hysteria|Hysteria2",
			},
		},
		Outbounds: []option.Outbound{
			{Type: C.TypeSelector, Tag: "proxy", Options: selectorOptions},
		},
	}

	if err := Expand(context.Background(), log.NewNOPFactory().Logger(), &options); err != nil {
		t.Fatal(err)
	}
	if got, want := selectorOptions.Outbounds, []string{"SS"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("selector outbounds = %#v, want %#v", got, want)
	}
}

func TestExpandOutboundGroupFiltersProviderUse(t *testing.T) {
	subscriptionPath := writeSubscription(t, `
proxies:
  - name: HK VLESS
    type: vless
    server: hk.example.com
    port: 443
    uuid: 00000000-0000-0000-0000-000000000001
    tls: true
  - name: HK HY2
    type: hysteria2
    server: hy2.example.com
    port: 443
    password: hy2-pass
  - name: JP VLESS
    type: vless
    server: jp.example.com
    port: 443
    uuid: 00000000-0000-0000-0000-000000000002
    tls: true
`)
	urlTestOptions := &option.URLTestOutboundOptions{
		Use:         []string{"sub"},
		Filter:      "HK",
		ExcludeType: "Hysteria|Hysteria2",
	}
	selectorOptions := &option.SelectorOutboundOptions{
		Outbounds: []string{"DIRECT"},
		Use:       []string{"sub"},
		Filter:    "JP",
	}
	options := option.Options{
		ProxyProviders: map[string]option.ProxyProvider{
			"sub": {
				Type: "file",
				Path: subscriptionPath,
			},
		},
		Outbounds: []option.Outbound{
			{Type: C.TypeDirect, Tag: "DIRECT"},
			{Type: C.TypeURLTest, Tag: "auto-hk", Options: urlTestOptions},
			{Type: C.TypeSelector, Tag: "manual-jp", Options: selectorOptions},
		},
	}

	if err := Expand(context.Background(), log.NewNOPFactory().Logger(), &options); err != nil {
		t.Fatal(err)
	}
	if got, want := urlTestOptions.Outbounds, []string{"HK VLESS"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("urltest outbounds = %#v, want %#v", got, want)
	}
	if got, want := selectorOptions.Outbounds, []string{"DIRECT", "JP VLESS"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("selector outbounds = %#v, want %#v", got, want)
	}
}

func TestExpandAddsBlockFallbackForEmptyOutboundGroupAfterFiltering(t *testing.T) {
	subscriptionPath := writeSubscription(t, `
proxies:
  - name: HK VLESS
    type: vless
    server: hk.example.com
    port: 443
    uuid: 00000000-0000-0000-0000-000000000001
    tls: true
`)
	selectorOptions := &option.SelectorOutboundOptions{
		Use:    []string{"sub"},
		Filter: "NO_MATCH",
	}
	urlTestOptions := &option.URLTestOutboundOptions{
		Use:    []string{"sub"},
		Filter: "NO_MATCH",
	}
	options := option.Options{
		ProxyProviders: map[string]option.ProxyProvider{
			"sub": {
				Type: "file",
				Path: subscriptionPath,
			},
		},
		Outbounds: []option.Outbound{
			{Type: C.TypeSelector, Tag: "empty-selector", Options: selectorOptions},
			{Type: C.TypeURLTest, Tag: "empty-urltest", Options: urlTestOptions},
		},
	}

	if err := Expand(context.Background(), log.NewNOPFactory().Logger(), &options); err != nil {
		t.Fatal(err)
	}
	if got, want := selectorOptions.Outbounds, []string{"empty-outbound-group"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("selector outbounds = %#v, want %#v", got, want)
	}
	if got, want := urlTestOptions.Outbounds, []string{"empty-outbound-group"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("urltest outbounds = %#v, want %#v", got, want)
	}
	if len(options.Outbounds) != 4 || options.Outbounds[3].Type != C.TypeBlock || options.Outbounds[3].Tag != "empty-outbound-group" {
		t.Fatalf("fallback outbound = %#v", options.Outbounds)
	}
}

func TestExpandUseWildcardIncludesAllProviders(t *testing.T) {
	providerAPath := writeSubscription(t, `
proxies:
  - name: HK VLESS
    type: vless
    server: hk.example.com
    port: 443
    uuid: 00000000-0000-0000-0000-000000000001
    tls: true
`)
	providerBPath := writeSubscription(t, `
proxies:
  - name: JP VLESS
    type: vless
    server: jp.example.com
    port: 443
    uuid: 00000000-0000-0000-0000-000000000002
    tls: true
`)
	selectorOptions := &option.SelectorOutboundOptions{
		Outbounds: []string{"DIRECT"},
		Use:       []string{"*"},
	}
	options := option.Options{
		ProxyProviders: map[string]option.ProxyProvider{
			"a": {
				Type: "file",
				Path: providerAPath,
				Override: option.ProxyProviderOverride{
					AdditionalPrefix: "A | ",
				},
			},
			"b": {
				Type: "file",
				Path: providerBPath,
				Override: option.ProxyProviderOverride{
					AdditionalPrefix: "B | ",
				},
			},
		},
		Outbounds: []option.Outbound{
			{Type: C.TypeDirect, Tag: "DIRECT"},
			{Type: C.TypeSelector, Tag: "all", Options: selectorOptions},
		},
	}

	if err := Expand(context.Background(), log.NewNOPFactory().Logger(), &options); err != nil {
		t.Fatal(err)
	}
	want := []string{"DIRECT", "A | HK VLESS", "B | JP VLESS"}
	if got := selectorOptions.Outbounds; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("selector outbounds = %#v, want %#v", got, want)
	}
}

func TestExpandImplicitAllProvidersForFilteredGroup(t *testing.T) {
	providerAPath := writeSubscription(t, `
proxies:
  - name: HK VLESS
    type: vless
    server: hk.example.com
    port: 443
    uuid: 00000000-0000-0000-0000-000000000001
    tls: true
`)
	providerBPath := writeSubscription(t, `
proxies:
  - name: JP VLESS
    type: vless
    server: jp.example.com
    port: 443
    uuid: 00000000-0000-0000-0000-000000000002
    tls: true
`)
	selectorOptions := &option.SelectorOutboundOptions{
		Filter: "VLESS",
	}
	options := option.Options{
		ProxyProviders: map[string]option.ProxyProvider{
			"a": {
				Type: "file",
				Path: providerAPath,
				Override: option.ProxyProviderOverride{
					AdditionalPrefix: "A | ",
				},
			},
			"b": {
				Type: "file",
				Path: providerBPath,
				Override: option.ProxyProviderOverride{
					AdditionalPrefix: "B | ",
				},
			},
		},
		Outbounds: []option.Outbound{
			{Type: C.TypeSelector, Tag: "all-filtered", Options: selectorOptions},
		},
	}

	if err := Expand(context.Background(), log.NewNOPFactory().Logger(), &options); err != nil {
		t.Fatal(err)
	}
	want := []string{"A | HK VLESS", "B | JP VLESS"}
	if got := selectorOptions.Outbounds; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("selector outbounds = %#v, want %#v", got, want)
	}
}

func TestExpandOutboundGroupExcludeFilterMatchesProviderTag(t *testing.T) {
	yepFastPath := writeSubscription(t, `
proxies:
  - name: 香港 01
    type: anytls
    server: yep.example.com
    port: 443
    password: yep-pass
  - name: 日本 01
    type: anytls
    server: yep-jp.example.com
    port: 443
    password: yep-jp-pass
`)
	otherPath := writeSubscription(t, `
proxies:
  - name: 香港 01
    type: vless
    server: hk.example.com
    port: 443
    uuid: 00000000-0000-0000-0000-000000000001
    tls: true
`)
	urlTestOptions := &option.URLTestOutboundOptions{
		Filter:        "香港",
		ExcludeFilter: "YepFast",
	}
	options := option.Options{
		ProxyProviders: map[string]option.ProxyProvider{
			"YepFast": {
				Type: "file",
				Path: yepFastPath,
				Override: option.ProxyProviderOverride{
					AdditionalPrefix: "YepFast | ",
				},
			},
			"云开见月": {
				Type: "file",
				Path: otherPath,
				Override: option.ProxyProviderOverride{
					AdditionalPrefix: "云开见月 | ",
				},
			},
		},
		Outbounds: []option.Outbound{
			{Type: C.TypeURLTest, Tag: "自动选择", Options: urlTestOptions},
		},
	}

	if err := Expand(context.Background(), log.NewNOPFactory().Logger(), &options); err != nil {
		t.Fatal(err)
	}
	if got, want := urlTestOptions.Outbounds, []string{"云开见月 | 香港 01"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("urltest outbounds = %#v, want %#v", got, want)
	}
}

func TestExpandSelectorFilterAllProvidersWithoutUse(t *testing.T) {
	providerAPath := writeSubscription(t, `
proxies:
  - name: HK VLESS
    type: vless
    server: hk.example.com
    port: 443
    uuid: 00000000-0000-0000-0000-000000000001
    tls: true
`)
	providerBPath := writeSubscription(t, `
proxies:
  - name: JP VLESS
    type: vless
    server: jp.example.com
    port: 443
    uuid: 00000000-0000-0000-0000-000000000002
    tls: true
`)
	selectorOptions := &option.SelectorOutboundOptions{
		Outbounds: []string{"自动选择"},
		Filter:    ".*",
	}
	options := option.Options{
		ProxyProviders: map[string]option.ProxyProvider{
			"a": {
				Type: "file",
				Path: providerAPath,
				Override: option.ProxyProviderOverride{
					AdditionalPrefix: "A | ",
				},
			},
			"b": {
				Type: "file",
				Path: providerBPath,
				Override: option.ProxyProviderOverride{
					AdditionalPrefix: "B | ",
				},
			},
		},
		Outbounds: []option.Outbound{
			{Type: C.TypeDirect, Tag: "自动选择"},
			{Type: C.TypeSelector, Tag: "默认代理", Options: selectorOptions},
		},
	}

	if err := Expand(context.Background(), log.NewNOPFactory().Logger(), &options); err != nil {
		t.Fatal(err)
	}
	want := []string{"自动选择", "A | HK VLESS", "B | JP VLESS"}
	if got := selectorOptions.Outbounds; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("selector outbounds = %#v, want %#v", got, want)
	}
}

func TestExpandPrunesMissingMembersFromProviderExpandedGroup(t *testing.T) {
	subscriptionPath := writeSubscription(t, `
proxies:
  - name: JP VLESS
    type: vless
    server: jp.example.com
    port: 443
    uuid: 00000000-0000-0000-0000-000000000002
    tls: true
`)
	selectorOptions := &option.SelectorOutboundOptions{
		Outbounds: []string{"DIRECT", "自动选择(香港外)"},
		Filter:    "JP",
		Default:   "自动选择(香港外)",
	}
	options := option.Options{
		ProxyProviders: map[string]option.ProxyProvider{
			"sub": {
				Type: "file",
				Path: subscriptionPath,
			},
		},
		Outbounds: []option.Outbound{
			{Type: C.TypeDirect, Tag: "DIRECT"},
			{Type: C.TypeSelector, Tag: "默认代理", Options: selectorOptions},
		},
	}

	if err := Expand(context.Background(), log.NewNOPFactory().Logger(), &options); err != nil {
		t.Fatal(err)
	}
	want := []string{"DIRECT", "JP VLESS"}
	if got := selectorOptions.Outbounds; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("selector outbounds = %#v, want %#v", got, want)
	}
	if selectorOptions.Default != "" {
		t.Fatalf("selector default = %q, want empty", selectorOptions.Default)
	}
}

func TestExpandHTTPProviderUsesFreshCacheWithoutFetching(t *testing.T) {
	cachePath := writeSubscription(t, `
proxies:
  - name: Cached SS
    type: ss
    server: cached.example.com
    port: 8388
    cipher: aes-128-gcm
    password: cached-pass
`)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Error(w, "unexpected fetch", http.StatusInternalServerError)
	}))
	defer server.Close()
	selectorOptions := &option.SelectorOutboundOptions{Use: []string{"sub"}}
	options := option.Options{
		ProxyProviders: map[string]option.ProxyProvider{
			"sub": {
				Type:     "http",
				URL:      server.URL,
				Path:     cachePath,
				Interval: 43200,
			},
		},
		Outbounds: []option.Outbound{
			{Type: C.TypeSelector, Tag: "proxy", Options: selectorOptions},
		},
	}

	if err := Expand(context.Background(), log.NewNOPFactory().Logger(), &options); err != nil {
		t.Fatal(err)
	}
	if requests != 0 {
		t.Fatalf("requests = %d, want 0", requests)
	}
	if got, want := selectorOptions.Outbounds, []string{"Cached SS"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("selector outbounds = %#v, want %#v", got, want)
	}
}

func TestExpandHTTPProviderDirectProxyFetchesDirectly(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_, _ = w.Write([]byte(`
proxies:
  - name: Direct SS
    type: ss
    server: direct.example.com
    port: 8388
    cipher: aes-128-gcm
    password: direct-pass
`))
	}))
	defer server.Close()
	selectorOptions := &option.SelectorOutboundOptions{Use: []string{"sub"}}
	options := option.Options{
		ProxyProviders: map[string]option.ProxyProvider{
			"sub": {
				Type:  "http",
				URL:   server.URL,
				Path:  filepath.Join(t.TempDir(), "subscription.yaml"),
				Proxy: "DIRECT",
			},
		},
		Outbounds: []option.Outbound{
			{Type: C.TypeSelector, Tag: "proxy", Options: selectorOptions},
		},
	}

	if err := Expand(context.Background(), log.NewNOPFactory().Logger(), &options); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
	if got, want := selectorOptions.Outbounds, []string{"Direct SS"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("selector outbounds = %#v, want %#v", got, want)
	}
}

func TestExpandHTTPProviderUnsupportedProxyUsesCachedContent(t *testing.T) {
	cachePath := writeSubscription(t, `
proxies:
  - name: Cached SS
    type: ss
    server: cached.example.com
    port: 8388
    cipher: aes-128-gcm
    password: cached-pass
`)
	oldTime := time.Now().Add(-time.Hour)
	if err := os.Chtimes(cachePath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Error(w, "unexpected fetch", http.StatusInternalServerError)
	}))
	defer server.Close()
	selectorOptions := &option.SelectorOutboundOptions{Use: []string{"sub"}}
	options := option.Options{
		ProxyProviders: map[string]option.ProxyProvider{
			"sub": {
				Type:  "http",
				URL:   server.URL,
				Path:  cachePath,
				Proxy: "默认代理",
			},
		},
		Outbounds: []option.Outbound{
			{Type: C.TypeSelector, Tag: "proxy", Options: selectorOptions},
		},
	}

	if err := Expand(context.Background(), log.NewNOPFactory().Logger(), &options); err != nil {
		t.Fatal(err)
	}
	if requests != 0 {
		t.Fatalf("requests = %d, want 0", requests)
	}
	if got, want := selectorOptions.Outbounds, []string{"Cached SS"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("selector outbounds = %#v, want %#v", got, want)
	}
}

func TestExpandHTTPProviderFetchFailureUsesCachedContent(t *testing.T) {
	cachePath := writeSubscription(t, `
proxies:
  - name: Cached SS
    type: ss
    server: cached.example.com
    port: 8388
    cipher: aes-128-gcm
    password: cached-pass
`)
	oldTime := time.Now().Add(-time.Hour)
	if err := os.Chtimes(cachePath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer server.Close()
	selectorOptions := &option.SelectorOutboundOptions{Use: []string{"sub"}}
	options := option.Options{
		ProxyProviders: map[string]option.ProxyProvider{
			"sub": {
				Type:     "http",
				URL:      server.URL,
				Path:     cachePath,
				Interval: 1,
			},
		},
		Outbounds: []option.Outbound{
			{Type: C.TypeSelector, Tag: "proxy", Options: selectorOptions},
		},
	}

	if err := Expand(context.Background(), log.NewNOPFactory().Logger(), &options); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
	if got, want := selectorOptions.Outbounds, []string{"Cached SS"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("selector outbounds = %#v, want %#v", got, want)
	}
}

func TestExpandHTTPProviderInvalidFetchedContentUsesCachedContent(t *testing.T) {
	cachePath := writeSubscription(t, `
proxies:
  - name: Cached SS
    type: ss
    server: cached.example.com
    port: 8388
    cipher: aes-128-gcm
    password: cached-pass
`)
	oldTime := time.Now().Add(-time.Hour)
	if err := os.Chtimes(cachePath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_, _ = w.Write([]byte(`<script>location.href="/login"</script>`))
	}))
	defer server.Close()
	selectorOptions := &option.SelectorOutboundOptions{Use: []string{"sub"}}
	options := option.Options{
		ProxyProviders: map[string]option.ProxyProvider{
			"sub": {
				Type:     "http",
				URL:      server.URL,
				Path:     cachePath,
				Interval: 1,
			},
		},
		Outbounds: []option.Outbound{
			{Type: C.TypeSelector, Tag: "proxy", Options: selectorOptions},
		},
	}

	if err := Expand(context.Background(), log.NewNOPFactory().Logger(), &options); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
	if got, want := selectorOptions.Outbounds, []string{"Cached SS"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("selector outbounds = %#v, want %#v", got, want)
	}
	cached, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(cached) == `<script>location.href="/login"</script>` {
		t.Fatal("invalid fetched content overwrote cache")
	}
}

func TestExpandHTTPProviderFetchFailureWithoutCacheSkipsProvider(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer server.Close()
	selectorOptions := &option.SelectorOutboundOptions{
		Outbounds: []string{"DIRECT"},
		Use:       []string{"sub"},
	}
	options := option.Options{
		ProxyProviders: map[string]option.ProxyProvider{
			"sub": {
				Type: "http",
				URL:  server.URL,
				Path: filepath.Join(t.TempDir(), "missing.yaml"),
			},
		},
		Outbounds: []option.Outbound{
			{Type: C.TypeDirect, Tag: "DIRECT"},
			{Type: C.TypeSelector, Tag: "proxy", Options: selectorOptions},
		},
	}

	if err := Expand(context.Background(), log.NewNOPFactory().Logger(), &options); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
	if got, want := selectorOptions.Outbounds, []string{"DIRECT"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("selector outbounds = %#v, want %#v", got, want)
	}
	if len(options.Outbounds) != 2 {
		t.Fatalf("outbound count = %d, want 2", len(options.Outbounds))
	}
}

func TestExpandHTTPProviderInvalidFetchedContentWithoutCacheSkipsProvider(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_, _ = w.Write([]byte(`<script>location.href="/login"</script>`))
	}))
	defer server.Close()
	selectorOptions := &option.SelectorOutboundOptions{
		Outbounds: []string{"DIRECT"},
		Use:       []string{"sub"},
	}
	options := option.Options{
		ProxyProviders: map[string]option.ProxyProvider{
			"sub": {
				Type: "http",
				URL:  server.URL,
				Path: filepath.Join(t.TempDir(), "missing.yaml"),
			},
		},
		Outbounds: []option.Outbound{
			{Type: C.TypeDirect, Tag: "DIRECT"},
			{Type: C.TypeSelector, Tag: "proxy", Options: selectorOptions},
		},
	}

	if err := Expand(context.Background(), log.NewNOPFactory().Logger(), &options); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
	if got, want := selectorOptions.Outbounds, []string{"DIRECT"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("selector outbounds = %#v, want %#v", got, want)
	}
	if len(options.Outbounds) != 2 {
		t.Fatalf("outbound count = %d, want 2", len(options.Outbounds))
	}
}

func TestExpandHTTPProviderInvalidFetchedContentAddsBlockFallbackForEmptyGroup(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<script>location.href="/login"</script>`))
	}))
	defer server.Close()
	selectorOptions := &option.SelectorOutboundOptions{Use: []string{"sub"}}
	options := option.Options{
		ProxyProviders: map[string]option.ProxyProvider{
			"sub": {
				Type: "http",
				URL:  server.URL,
				Path: filepath.Join(t.TempDir(), "missing.yaml"),
			},
		},
		Outbounds: []option.Outbound{
			{Type: C.TypeSelector, Tag: "proxy", Options: selectorOptions},
		},
	}

	if err := Expand(context.Background(), log.NewNOPFactory().Logger(), &options); err != nil {
		t.Fatal(err)
	}
	if got, want := selectorOptions.Outbounds, []string{"empty-outbound-group"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("selector outbounds = %#v, want %#v", got, want)
	}
	if len(options.Outbounds) != 2 || options.Outbounds[1].Type != C.TypeBlock || options.Outbounds[1].Tag != "empty-outbound-group" {
		t.Fatalf("fallback outbound = %#v", options.Outbounds)
	}
}

func TestExpandAddsBlockFallbackWhenGroupFilterMatchesNoProviderProxies(t *testing.T) {
	subscriptionPath := writeSubscription(t, `
proxies:
  - name: US SS
    type: ss
    server: us.example.com
    port: 8388
    cipher: aes-128-gcm
    password: us-pass
`)
	selectorOptions := &option.SelectorOutboundOptions{
		Use:    []string{"sub"},
		Filter: "HK",
	}
	options := option.Options{
		ProxyProviders: map[string]option.ProxyProvider{
			"sub": {
				Type: "file",
				Path: subscriptionPath,
			},
		},
		Outbounds: []option.Outbound{
			{Type: C.TypeSelector, Tag: "proxy", Options: selectorOptions},
		},
	}

	if err := Expand(context.Background(), log.NewNOPFactory().Logger(), &options); err != nil {
		t.Fatal(err)
	}
	if got, want := selectorOptions.Outbounds, []string{"empty-outbound-group"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("selector outbounds = %#v, want %#v", got, want)
	}
	if len(options.Outbounds) != 3 || options.Outbounds[2].Type != C.TypeBlock || options.Outbounds[2].Tag != "empty-outbound-group" {
		t.Fatalf("fallback outbound = %#v", options.Outbounds)
	}
}

func TestExpandHTTPProviderUnsupportedProxyWithoutCacheSkipsProvider(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Error(w, "unexpected fetch", http.StatusInternalServerError)
	}))
	defer server.Close()
	selectorOptions := &option.SelectorOutboundOptions{
		Outbounds: []string{"DIRECT"},
		Use:       []string{"sub"},
	}
	options := option.Options{
		ProxyProviders: map[string]option.ProxyProvider{
			"sub": {
				Type:  "http",
				URL:   server.URL,
				Path:  filepath.Join(t.TempDir(), "missing.yaml"),
				Proxy: "默认代理",
			},
		},
		Outbounds: []option.Outbound{
			{Type: C.TypeDirect, Tag: "DIRECT"},
			{Type: C.TypeSelector, Tag: "proxy", Options: selectorOptions},
		},
	}

	if err := Expand(context.Background(), log.NewNOPFactory().Logger(), &options); err != nil {
		t.Fatal(err)
	}
	if requests != 0 {
		t.Fatalf("requests = %d, want 0", requests)
	}
	if got, want := selectorOptions.Outbounds, []string{"DIRECT"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("selector outbounds = %#v, want %#v", got, want)
	}
	if len(options.Outbounds) != 2 {
		t.Fatalf("outbound count = %d, want 2", len(options.Outbounds))
	}
}

func TestExpandHTTPProviderFetchFailurePrunesUnavailableGroupMembers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer server.Close()
	selectorOptions := &option.SelectorOutboundOptions{
		Outbounds: []string{"DIRECT", "低倍率"},
		Default:   "低倍率",
	}
	options := option.Options{
		ProxyProviders: map[string]option.ProxyProvider{
			"sub": {
				Type: "http",
				URL:  server.URL,
				Path: filepath.Join(t.TempDir(), "missing.yaml"),
			},
		},
		Outbounds: []option.Outbound{
			{Type: C.TypeDirect, Tag: "DIRECT"},
			{Type: C.TypeSelector, Tag: "Apple", Options: selectorOptions},
		},
	}

	if err := Expand(context.Background(), log.NewNOPFactory().Logger(), &options); err != nil {
		t.Fatal(err)
	}
	if got, want := selectorOptions.Outbounds, []string{"DIRECT"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("selector outbounds = %#v, want %#v", got, want)
	}
	if selectorOptions.Default != "" {
		t.Fatalf("selector default = %q, want empty", selectorOptions.Default)
	}
}

func TestExpandPrunesMissingGroupMembersAfterSuccessfulProviderExpansion(t *testing.T) {
	subscriptionPath := writeSubscription(t, `
proxies:
  - name: HK SS
    type: ss
    server: hk.example.com
    port: 8388
    cipher: aes-128-gcm
    password: hk-pass
`)
	selectorOptions := &option.SelectorOutboundOptions{
		Outbounds: []string{"DIRECT", "低倍率"},
		Default:   "低倍率",
	}
	options := option.Options{
		ProxyProviders: map[string]option.ProxyProvider{
			"sub": {
				Type: "file",
				Path: subscriptionPath,
			},
		},
		Outbounds: []option.Outbound{
			{Type: C.TypeDirect, Tag: "DIRECT"},
			{Type: C.TypeSelector, Tag: "Apple", Options: selectorOptions},
		},
	}

	if err := Expand(context.Background(), log.NewNOPFactory().Logger(), &options); err != nil {
		t.Fatal(err)
	}
	if got, want := selectorOptions.Outbounds, []string{"DIRECT"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("selector outbounds = %#v, want %#v", got, want)
	}
	if selectorOptions.Default != "" {
		t.Fatalf("selector default = %q, want empty", selectorOptions.Default)
	}
}

func TestExpandFileProviderMissingPathRemainsFatal(t *testing.T) {
	options := option.Options{
		ProxyProviders: map[string]option.ProxyProvider{
			"sub": {
				Type: "file",
				Path: filepath.Join(t.TempDir(), "missing.yaml"),
			},
		},
		Outbounds: []option.Outbound{
			{Type: C.TypeSelector, Tag: "proxy", Options: &option.SelectorOutboundOptions{Use: []string{"sub"}}},
		},
	}

	if err := Expand(context.Background(), log.NewNOPFactory().Logger(), &options); err == nil {
		t.Fatal("expected missing file provider to fail")
	}
}

func writeSubscription(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "subscription.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

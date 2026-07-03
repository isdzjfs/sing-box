package proxyprovider

import (
	"context"
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
    sni: vless.example.com
    network: ws
    ws-opts:
      path: /ws
      headers:
        Host: cdn.example.com
`)
	enableUDP := true
	selectorOptions := &option.SelectorOutboundOptions{Use: []string{"sub"}}
	urlTestOptions := &option.URLTestOutboundOptions{Use: []string{"sub"}}
	options := option.Options{
		ProxyProviderDefaults: &option.ProxyProvider{
			ExcludeFilter: "流量|到期",
			Override: option.ProxyProviderOverride{
				UDP:       &enableUDP,
				IPVersion: "ipv4",
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
	if vlessOptions.Transport == nil || vlessOptions.Transport.Type != C.V2RayTransportTypeWebsocket {
		t.Fatalf("unexpected transport: %#v", vlessOptions.Transport)
	}
	if vlessOptions.Transport.WebsocketOptions.Path != "/ws" {
		t.Fatalf("ws path = %q", vlessOptions.Transport.WebsocketOptions.Path)
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
  - name: Shared
    type: ss
    server: ss.example.com
    port: 8388
    cipher: aes-128-gcm
    password: ss-pass
  - name: Shared
    type: wireguard
    server: wireguard.example.com
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

func TestExpandAllowsEmptyOutboundGroupAfterFiltering(t *testing.T) {
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
	if len(selectorOptions.Outbounds) != 0 {
		t.Fatalf("selector outbounds = %#v, want empty", selectorOptions.Outbounds)
	}
	if len(urlTestOptions.Outbounds) != 0 {
		t.Fatalf("urltest outbounds = %#v, want empty", urlTestOptions.Outbounds)
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

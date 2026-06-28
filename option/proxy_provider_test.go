package option

import (
	"context"
	"testing"
)

func TestOptionsUnmarshalProxyProviders(t *testing.T) {
	var options Options
	err := options.UnmarshalJSONContext(context.Background(), []byte(`{
		"proxy-provider-defaults": {
			"type": "http",
			"interval": 43200,
			"exclude-filter": "流量|到期",
			"exclude-type": "Hysteria|Hysteria2",
			"override": {
				"udp": true,
				"ip-version": "ipv4"
			}
		},
		"proxy-providers": {
			"sub": {
				"url": "https://example.com/sub.yaml",
				"proxy": "",
				"override": {
					"additional-prefix": "良心云 | "
				}
			}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if options.ProxyProviderDefaults == nil {
		t.Fatal("missing proxy provider defaults")
	}
	if options.ProxyProviderDefaults.Interval != 43200 || options.ProxyProviderDefaults.ExcludeFilter != "流量|到期" {
		t.Fatalf("unexpected provider defaults: %#v", options.ProxyProviderDefaults)
	}
	provider := options.ProxyProviders["sub"]
	if provider.URL != "https://example.com/sub.yaml" {
		t.Fatalf("unexpected provider: %#v", provider)
	}
	if provider.Override.AdditionalPrefix != "良心云 | " {
		t.Fatalf("unexpected override: %#v", provider.Override)
	}
}

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
			"exclude-filter": "traffic|expire",
			"exclude-type": "Hysteria|Hysteria2",
			"override": {
				"udp": true,
				"ip-version": "ipv4",
				"insecure": false,
				"client_name": "DefaultClient"
			}
		},
		"proxy-providers": {
			"sub": {
				"url": "https://example.com/sub.yaml",
				"proxy": "",
				"override": {
					"client_name": "ProviderClient",
					"additional-prefix": "Provider A | "
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
	if options.ProxyProviderDefaults.Interval != 43200 || options.ProxyProviderDefaults.ExcludeFilter != "traffic|expire" {
		t.Fatalf("unexpected provider defaults: %#v", options.ProxyProviderDefaults)
	}
	if options.ProxyProviderDefaults.Override.Insecure == nil || *options.ProxyProviderDefaults.Override.Insecure {
		t.Fatalf("unexpected default override insecure: %#v", options.ProxyProviderDefaults.Override.Insecure)
	}
	if options.ProxyProviderDefaults.Override.ClientName != "DefaultClient" {
		t.Fatalf("unexpected default override client name: %#v", options.ProxyProviderDefaults.Override)
	}
	provider := options.ProxyProviders["sub"]
	if provider.URL != "https://example.com/sub.yaml" {
		t.Fatalf("unexpected provider: %#v", provider)
	}
	if provider.Override.ClientName != "ProviderClient" {
		t.Fatalf("unexpected provider client name override: %#v", provider.Override)
	}
	if provider.Override.AdditionalPrefix != "Provider A | " {
		t.Fatalf("unexpected override: %#v", provider.Override)
	}
}

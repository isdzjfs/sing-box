package rule

import (
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/stretchr/testify/require"
)

func TestFormatHTTPTransportDescriptionIncludesDownloadPath(t *testing.T) {
	description := formatHTTPTransportDescription(adapter.HTTPTransportInfo{
		Tag:     "__senvpn_rule_set_download",
		Detour:  "Hong Kong IEPL 1",
		Engine:  "go",
		Version: 2,
	}, "Hong Kong IEPL 1", "shadowsocks")

	require.Equal(
		t,
		"http_client[__senvpn_rule_set_download] detour[Hong Kong IEPL 1] outbound_type[shadowsocks] engine[go] http_version[2]",
		description,
	)
}

func TestFormatHTTPTransportDescriptionMarksImplicitDefault(t *testing.T) {
	description := formatHTTPTransportDescription(adapter.HTTPTransportInfo{
		Engine:          "go",
		Version:         2,
		DefaultOutbound: true,
	}, "proxy", "selector")

	require.Equal(
		t,
		"http_client[implicit-default] detour[proxy] outbound_type[selector] engine[go] http_version[2]",
		description,
	)
}

func TestFormatHTTPTransportDescriptionSanitizesUntrustedTags(t *testing.T) {
	description := formatHTTPTransportDescription(adapter.HTTPTransportInfo{
		Tag:     "client\nforged",
		Detour:  "node\rname",
		Engine:  "go",
		Version: 2,
	}, "node\rname", "shadow\tsocks")

	require.Equal(
		t,
		"http_client[client forged] detour[node name] outbound_type[shadow socks] engine[go] http_version[2]",
		description,
	)
}

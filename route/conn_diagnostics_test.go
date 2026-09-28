package route

import (
	"net"
	"strings"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/stretchr/testify/require"
)

func TestFormatConnectionDiagnostics(t *testing.T) {
	group := &connectionDiagnosticsTestOutbound{outboundType: "urltest", tag: "auto"}
	leaf := &connectionDiagnosticsTestOutbound{outboundType: "vless", tag: "hk-node"}
	metadata := adapter.InboundContext{
		Destination:   M.ParseSocksaddr("198.51.100.9:443"),
		Domain:        "service.example",
		Protocol:      "tls",
		RouteOutbound: "auto",
		OutboundChain: []adapter.Outbound{group, leaf},
	}
	leafConn := &connectionDiagnosticsTestConn{
		selectedType: "vless",
		selectedTag:  "sg-node",
	}
	remoteConn := &connectionDiagnosticsTestConn{
		Conn:         leafConn,
		remoteAddr:   &net.TCPAddr{IP: net.ParseIP("203.0.113.7"), Port: 443},
		selectedType: "selector",
		selectedTag:  "manual",
	}

	diagnostics := formatConnectionDiagnostics(metadata, group, remoteConn, remoteConn.RemoteAddr())

	require.Equal(t, ` (destination="198.51.100.9:443", domain="service.example", protocol="tls", route_outbound="auto", dialer="urltest[auto]", route_chain="urltest[auto] > vless[hk-node]", selected_chain="selector[manual] > vless[sg-node]", remote="203.0.113.7:443")`, diagnostics)
}

func TestFormatConnectionDiagnosticsUsesCapturedRemoteAddress(t *testing.T) {
	remoteAddress := &net.TCPAddr{IP: net.ParseIP("203.0.113.7"), Port: 443}

	diagnostics := formatConnectionDiagnostics(adapter.InboundContext{}, nil, &connectionDiagnosticsTestConn{}, remoteAddress)

	require.Equal(t, ` (remote="203.0.113.7:443")`, diagnostics)
}

func TestConnectionDiagnosticFieldIsSingleLineAndBounded(t *testing.T) {
	field := connectionDiagnosticField("tag", "unsafe\n"+strings.Repeat("x", 300))

	require.NotContains(t, field, "\n")
	require.Contains(t, field, `unsafe\n`)
	require.Less(t, len([]rune(field)), 280)
}

type connectionDiagnosticsTestOutbound struct {
	adapter.Outbound
	outboundType string
	tag          string
}

func (o *connectionDiagnosticsTestOutbound) Type() string { return o.outboundType }
func (o *connectionDiagnosticsTestOutbound) Tag() string  { return o.tag }

type connectionDiagnosticsTestConn struct {
	net.Conn
	remoteAddr   net.Addr
	selectedType string
	selectedTag  string
}

func (c *connectionDiagnosticsTestConn) RemoteAddr() net.Addr {
	return c.remoteAddr
}

func (c *connectionDiagnosticsTestConn) SelectedOutbound() (string, string, bool) {
	return c.selectedType, c.selectedTag, c.selectedType != "" || c.selectedTag != ""
}

func (c *connectionDiagnosticsTestConn) Upstream() any {
	return c.Conn
}

package route

import (
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/bufio"
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

func TestTCPPrefixFailureKeepsSelectedOutboundAndCause(t *testing.T) {
	remote, peer := net.Pipe()
	require.NoError(t, peer.Close())
	actual := &connectionDiagnosticsTestConn{
		Conn:         remote,
		remoteAddr:   &net.TCPAddr{IP: net.ParseIP("203.0.113.7"), Port: 443},
		selectedType: "vless",
		selectedTag:  "actual",
	}
	group := &connectionDiagnosticsTestOutbound{outboundType: "urltest", tag: "auto", conn: actual}
	inbound, client := net.Pipe()
	defer client.Close()
	var failure error
	NewConnectionManager(log.NewNOPFactory().Logger()).NewConnection(t.Context(), group, inbound, adapter.InboundContext{
		Destination:   M.ParseSocksaddr("service.example:443"),
		RouteOutbound: "auto",
		TCPPrefix:     []byte("private-prefix"),
	}, func(err error) { failure = err })

	require.ErrorIs(t, failure, io.ErrClosedPipe)
	require.ErrorContains(t, failure, "write TCP prefix")
	require.ErrorContains(t, failure, `route_outbound="auto"`)
	require.ErrorContains(t, failure, `selected_chain="vless[actual]"`)
	require.ErrorContains(t, failure, `remote="203.0.113.7:443"`)
	require.NotContains(t, failure.Error(), "private-prefix")
}

func TestTCPPrefixPrecedesBufferedPayload(t *testing.T) {
	remote, peer := net.Pipe()
	defer peer.Close()
	inbound, client := net.Pipe()
	defer client.Close()
	group := &connectionDiagnosticsTestOutbound{conn: remote}
	inbound = bufio.NewCachedConn(inbound, buf.As([]byte("payload")))
	go NewConnectionManager(log.NewNOPFactory().Logger()).NewConnection(t.Context(), group, inbound, adapter.InboundContext{
		Destination: M.ParseSocksaddr("service.example:443"),
		TCPPrefix:   []byte("prefix"),
	}, nil)

	require.NoError(t, peer.SetReadDeadline(time.Now().Add(5*time.Second)))
	received := make([]byte, len("prefixpayload"))
	_, err := io.ReadFull(peer, received)
	require.NoError(t, err)
	require.Equal(t, "prefixpayload", string(received))
}

type connectionDiagnosticsTestOutbound struct {
	adapter.Outbound
	outboundType string
	tag          string
	conn         net.Conn
}

func (o *connectionDiagnosticsTestOutbound) Type() string { return o.outboundType }
func (o *connectionDiagnosticsTestOutbound) Tag() string  { return o.tag }
func (o *connectionDiagnosticsTestOutbound) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return o.conn, nil
}

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

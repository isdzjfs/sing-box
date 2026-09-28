package route

import (
	"testing"

	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"

	mDNS "github.com/miekg/dns"
	"github.com/stretchr/testify/require"
)

func TestFormatDNSPacketContext(t *testing.T) {
	message := new(mDNS.Msg)
	message.SetQuestion("example.com.", mDNS.TypeA)
	metadata := adapter.InboundContext{
		Network:                  "udp",
		InboundType:              "tun",
		Inbound:                  "tun-in",
		Source:                   M.ParseSocksaddr("192.0.2.10:53000"),
		Destination:              M.ParseSocksaddr("8.8.8.8:53"),
		RouteOriginalDestination: M.ParseSocksaddr("1.1.1.1:53"),
	}

	diagnostics := formatDNSPacketContext(metadata, message)

	require.Equal(t, ` (network="udp", inbound="tun[tun-in]", source="192.0.2.10:53000", destination="8.8.8.8:53", original_destination="1.1.1.1:53", query="example.com. IN A")`, diagnostics)
}

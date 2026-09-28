package dns

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/stretchr/testify/require"
)

func TestHandleStreamDNSRequestReportsTruncatedRequestContext(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})
	go func() {
		_ = binary.Write(clientConn, binary.BigEndian, uint16(12))
		_, _ = clientConn.Write(make([]byte, 5))
		_ = clientConn.Close()
	}()

	err := HandleStreamDNSRequest(context.Background(), nil, serverConn, adapter.InboundContext{
		Source: M.ParseSocksaddr("192.0.2.10:53000"),
	})

	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	require.EqualError(t, err, `read TCP DNS request payload (source="192.0.2.10:53000", declared_length=12): unexpected EOF`)
}

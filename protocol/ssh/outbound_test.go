package ssh

import (
	"context"
	"net"
	"testing"
	"testing/synctest"
	"time"

	C "github.com/sagernet/sing-box/constant"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/stretchr/testify/require"
)

func TestSharedSSHHandshakeUsesServiceTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		conn, silentServer := net.Pipe()
		defer silentServer.Close()
		start := time.Now()
		require.NoError(t, conn.SetDeadline(start.Add(2*C.TCPTimeout)))
		outbound := &Outbound{ctx: t.Context(), dialer: sshHandshakeTestDialer{conn: conn}}
		stream, cancel := context.WithCancel(t.Context())
		cancel()

		_, err := outbound.DialContext(stream, N.NetworkTCP, M.ParseSocksaddr("example.invalid:443"))
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Equal(t, C.TCPTimeout, time.Since(start))
		require.Nil(t, outbound.client)
		require.Nil(t, outbound.clientConn)
	})
}

type sshHandshakeTestDialer struct {
	N.Dialer
	conn net.Conn
}

func (d sshHandshakeTestDialer) DialContext(ctx context.Context, _ string, _ M.Socksaddr) (net.Conn, error) {
	return d.conn, ctx.Err()
}

package dns

import (
	"context"
	"io"
	"strings"
	"testing"

	mDNS "github.com/miekg/dns"
	"github.com/stretchr/testify/require"
)

func TestExchangeToTransportErrorIncludesQueryAndTransport(t *testing.T) {
	transport := &fakeDNSTransport{
		tag:         "resolver\n" + strings.Repeat("x", 300),
		exchangeErr: io.ErrUnexpectedEOF,
	}
	message := new(mDNS.Msg)
	message.SetQuestion("example.com.", mDNS.TypeAAAA)

	response, err := (&Client{}).exchangeToTransport(context.Background(), transport, message)

	require.Nil(t, response)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	require.Contains(t, err.Error(), `query="example.com. IN AAAA"`)
	require.Contains(t, err.Error(), `transport="fake[resolver\n`)
	require.NotContains(t, err.Error(), "\n")
	require.Less(t, len([]rune(err.Error())), 600)
}

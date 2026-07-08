//go:build !with_quic

package v2rayxhttp

import (
	"context"
	"net/http"
	"time"

	boxTLS "github.com/sagernet/sing-box/common/tls"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

func newHTTP3RoundTripper(context.Context, N.Dialer, M.Socksaddr, boxTLS.Config, time.Duration) (http.RoundTripper, error) {
	return nil, E.New("xhttp HTTP/3 requires building with the with_quic tag")
}

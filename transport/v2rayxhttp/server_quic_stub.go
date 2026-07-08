//go:build !with_quic

package v2rayxhttp

import (
	"context"
	"net/http"

	boxTLS "github.com/sagernet/sing-box/common/tls"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
)

func newHTTP3Server(context.Context, logger.ContextLogger, boxTLS.ServerConfig, http.Handler) (http3Server, error) {
	return nil, E.New("xhttp HTTP/3 requires building with the with_quic tag")
}

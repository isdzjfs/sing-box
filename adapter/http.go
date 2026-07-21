package adapter

import (
	"context"
	"net/http"
	"sync"

	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/logger"
)

type HTTPTransport interface {
	http.RoundTripper
	CloseIdleConnections()
	Reset()
	Info() HTTPTransportInfo
}

// HTTPTransportInfo contains only non-secret fields that are safe to include in
// request failure logs. In particular, headers, credentials and TLS material are
// intentionally excluded.
type HTTPTransportInfo struct {
	Tag             string
	Detour          string
	Engine          string
	Version         int
	DefaultOutbound bool
}

type HTTPClientManager interface {
	ResolveTransport(ctx context.Context, logger logger.ContextLogger, options option.HTTPClientOptions) (HTTPTransport, error)
	DefaultTransport() HTTPTransport
	ResetNetwork()
}

type HTTPStartContext struct {
	access     sync.Mutex
	transports []HTTPTransport
}

func NewHTTPStartContext() *HTTPStartContext {
	return &HTTPStartContext{}
}

func (c *HTTPStartContext) Register(transport HTTPTransport) {
	c.access.Lock()
	defer c.access.Unlock()
	c.transports = append(c.transports, transport)
}

func (c *HTTPStartContext) Close() {
	for _, transport := range c.transports {
		transport.CloseIdleConnections()
	}
}

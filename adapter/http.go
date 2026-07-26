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
}

type HTTPClientManager interface {
	ResolveTransport(ctx context.Context, logger logger.ContextLogger, options option.HTTPClientOptions) (HTTPTransport, error)
	DefaultTransport() HTTPTransport
	// DirectTransport returns a reference to one shared detour-free transport, created on first
	// use. Callers that need a connection outside the tunnel — rule-set fallbacks, most visibly —
	// would otherwise each get a transport of their own that stays tracked until shutdown.
	DirectTransport() (HTTPTransport, error)
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

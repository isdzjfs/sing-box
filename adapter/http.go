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
	// DirectTransport returns a shared detour-free transport. When preserveDefault is true, empty
	// options inherit the configured default HTTP client's request semantics. Routing-related
	// dialer fields are always removed, while headers, TLS, and HTTP version settings are retained.
	DirectTransport(options *option.HTTPClientOptions, preserveDefault bool) (HTTPTransport, error)
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

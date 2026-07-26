package httpclient

import (
	"context"
	"sync"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
)

var (
	_ adapter.HTTPClientManager = (*Manager)(nil)
	_ adapter.LifecycleService  = (*Manager)(nil)
)

type Manager struct {
	ctx                      context.Context
	logger                   log.ContextLogger
	access                   sync.Mutex
	defines                  map[string]option.HTTPClient
	sharedTransports         map[string]*sharedManagedTransport
	managedTransports        []*ManagedTransport
	defaultTag               string
	defaultTransport         *sharedManagedTransport
	directTransports         map[string]*sharedManagedTransport
	defaultTransportFallback func() (*ManagedTransport, error)
}

type sharedManagedTransport struct {
	managed *ManagedTransport
	shared  *sharedState
}

func NewManager(ctx context.Context, logger log.ContextLogger, clients []option.HTTPClient, defaultHTTPClient string) *Manager {
	defines := make(map[string]option.HTTPClient, len(clients))
	for _, client := range clients {
		defines[client.Tag] = client
	}
	defaultTag := defaultHTTPClient
	if defaultTag == "" && len(clients) > 0 {
		defaultTag = clients[0].Tag
	}
	return &Manager{
		ctx:              ctx,
		logger:           logger,
		defines:          defines,
		sharedTransports: make(map[string]*sharedManagedTransport),
		directTransports: make(map[string]*sharedManagedTransport),
		defaultTag:       defaultTag,
	}
}

func (m *Manager) Initialize(defaultTransportFallback func() (*ManagedTransport, error)) {
	m.defaultTransportFallback = defaultTransportFallback
}

func (m *Manager) Name() string {
	return "http-client"
}

func (m *Manager) Start(stage adapter.StartStage) error {
	if stage != adapter.StartStateStart {
		return nil
	}
	if m.defaultTag != "" {
		sharedTransport, err := m.resolveShared(m.defaultTag)
		if err != nil {
			return E.Cause(err, "resolve default http client")
		}
		m.defaultTransport = sharedTransport
	}
	return nil
}

func (m *Manager) DefaultTransport() adapter.HTTPTransport {
	m.access.Lock()
	defer m.access.Unlock()
	if m.defaultTransport == nil && m.defaultTransportFallback != nil {
		transport, err := m.defaultTransportFallback()
		if err != nil {
			m.logger.Error(E.Cause(err, "create default http client"))
			return nil
		}
		m.managedTransports = append(m.managedTransports, transport)
		m.defaultTransport = &sharedManagedTransport{
			managed: transport,
			shared:  &sharedState{},
		}
	}
	if m.defaultTransport == nil {
		return nil
	}
	return newSharedRef(m.defaultTransport.managed, m.defaultTransport.shared)
}

// DirectTransport shares transports by their effective request semantics. Routing fields are
// stripped so the connection is genuinely direct, while an origin fallback can retain headers,
// TLS, and protocol settings from the configured client.
func (m *Manager) DirectTransport(options *option.HTTPClientOptions, preserveDefault bool) (adapter.HTTPTransport, error) {
	m.access.Lock()
	defer m.access.Unlock()
	directOptions, err := m.resolveDirectOptions(options, preserveDefault)
	if err != nil {
		return nil, err
	}
	identity := transportCacheIdentity(directOptions)
	if sharedTransport, loaded := m.directTransports[identity]; loaded {
		return newSharedRef(sharedTransport.managed, sharedTransport.shared), nil
	}
	transport, err := NewTransport(m.ctx, m.logger, "", directOptions)
	if err != nil {
		return nil, E.Cause(err, "create direct http client")
	}
	sharedTransport := &sharedManagedTransport{
		managed: transport,
		shared:  &sharedState{},
	}
	m.directTransports[identity] = sharedTransport
	m.managedTransports = append(m.managedTransports, transport)
	return newSharedRef(sharedTransport.managed, sharedTransport.shared), nil
}

func (m *Manager) resolveDirectOptions(options *option.HTTPClientOptions, preserveDefault bool) (option.HTTPClientOptions, error) {
	var resolved option.HTTPClientOptions
	if options != nil && !options.IsEmpty() {
		if options.Tag != "" {
			define, loaded := m.defines[options.Tag]
			if !loaded {
				return option.HTTPClientOptions{}, E.New("http_client not found: ", options.Tag)
			}
			resolved = define.Options()
		} else {
			resolved = *options
		}
	} else if preserveDefault && m.defaultTag != "" {
		define, loaded := m.defines[m.defaultTag]
		if !loaded {
			return option.HTTPClientOptions{}, E.New("http_client not found: ", m.defaultTag)
		}
		resolved = define.Options()
	}
	resolved.Tag = ""
	resolved.DialerOptions = option.DialerOptions{}
	resolved.DefaultOutbound = false
	resolved.DisableEmptyDirectCheck = false
	resolved.ResolveOnDetour = false
	resolved.DirectResolver = false
	return resolved, nil
}

func (m *Manager) ResolveTransport(ctx context.Context, logger logger.ContextLogger, options option.HTTPClientOptions) (adapter.HTTPTransport, error) {
	if options.Tag != "" {
		if options.ResolveOnDetour {
			define, loaded := m.defines[options.Tag]
			if !loaded {
				return nil, E.New("http_client not found: ", options.Tag)
			}
			resolvedOptions := define.Options()
			resolvedOptions.ResolveOnDetour = true
			transport, err := NewTransport(ctx, logger, options.Tag, resolvedOptions)
			if err != nil {
				return nil, err
			}
			m.trackTransport(transport)
			return transport, nil
		}
		sharedTransport, err := m.resolveShared(options.Tag)
		if err != nil {
			return nil, err
		}
		return newSharedRef(sharedTransport.managed, sharedTransport.shared), nil
	}
	transport, err := NewTransport(ctx, logger, "", options)
	if err != nil {
		return nil, err
	}
	m.trackTransport(transport)
	return transport, nil
}

func (m *Manager) trackTransport(transport *ManagedTransport) {
	m.access.Lock()
	defer m.access.Unlock()
	m.managedTransports = append(m.managedTransports, transport)
}

func (m *Manager) resolveShared(tag string) (*sharedManagedTransport, error) {
	m.access.Lock()
	defer m.access.Unlock()
	if sharedTransport, loaded := m.sharedTransports[tag]; loaded {
		return sharedTransport, nil
	}
	define, loaded := m.defines[tag]
	if !loaded {
		return nil, E.New("http_client not found: ", tag)
	}
	transport, err := NewTransport(m.ctx, m.logger, tag, define.Options())
	if err != nil {
		return nil, E.Cause(err, "create shared http_client[", tag, "]")
	}
	sharedTransport := &sharedManagedTransport{
		managed: transport,
		shared:  &sharedState{},
	}
	m.sharedTransports[tag] = sharedTransport
	m.managedTransports = append(m.managedTransports, transport)
	return sharedTransport, nil
}

func (m *Manager) ResetNetwork() {
	m.access.Lock()
	defer m.access.Unlock()
	for _, transport := range m.managedTransports {
		transport.Reset()
	}
}

func (m *Manager) Close() error {
	m.access.Lock()
	defer m.access.Unlock()
	if m.managedTransports == nil {
		return nil
	}
	var err error
	for _, transport := range m.managedTransports {
		err = E.Append(err, transport.close(), func(err error) error {
			return E.Cause(err, "close http client")
		})
	}
	m.managedTransports = nil
	m.sharedTransports = nil
	m.directTransports = nil
	return err
}

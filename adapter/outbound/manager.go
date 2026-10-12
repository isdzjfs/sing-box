package outbound

import (
	"context"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
)

var _ adapter.OutboundManager = (*Manager)(nil)

type Manager struct {
	logger                  log.ContextLogger
	registry                adapter.OutboundRegistry
	endpoint                adapter.EndpointManager
	defaultTag              string
	access                  sync.RWMutex
	lifecycleAccess         sync.Mutex
	started                 bool
	closed                  bool
	stage                   adapter.StartStage
	scope                   *adapter.Scope
	scopes                  map[adapter.Outbound]*adapter.Scope
	outbounds               []adapter.Outbound
	outboundByTag           map[string]adapter.Outbound
	dependByTag             map[string][]string
	defaultOutbound         adapter.Outbound
	defaultOutboundFallback func() (adapter.Outbound, error)
}

func NewManager(logger log.ContextLogger, registry adapter.OutboundRegistry, endpoint adapter.EndpointManager, defaultTag string) *Manager {
	return &Manager{
		logger:        logger,
		registry:      registry,
		endpoint:      endpoint,
		defaultTag:    defaultTag,
		outboundByTag: make(map[string]adapter.Outbound),
		dependByTag:   make(map[string][]string),
		scopes:        make(map[adapter.Outbound]*adapter.Scope),
	}
}

func (m *Manager) Initialize(defaultOutboundFallback func() (adapter.Outbound, error)) {
	m.defaultOutboundFallback = defaultOutboundFallback
}

func (m *Manager) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	m.lifecycleAccess.Lock()
	defer m.lifecycleAccess.Unlock()
	if m.closed {
		return os.ErrClosed
	}
	m.access.Lock()
	m.started = true
	m.stage = stage
	m.scope = scope
	if stage == adapter.StartStateInitialize {
		scope.Add(m.Close)
		if m.defaultTag != "" && m.defaultOutbound == nil {
			defaultEndpoint, loaded := m.endpoint.Get(m.defaultTag)
			if !loaded {
				m.access.Unlock()
				return E.New("default outbound not found: ", m.defaultTag)
			}
			m.defaultOutbound = defaultEndpoint
		}
		if m.defaultOutbound == nil {
			directOutbound, err := m.defaultOutboundFallback()
			if err != nil {
				m.access.Unlock()
				return E.Cause(err, "create direct outbound for fallback")
			}
			m.outbounds = append(m.outbounds, directOutbound)
			m.outboundByTag[directOutbound.Tag()] = directOutbound
			m.defaultOutbound = directOutbound
		}
	}
	outbounds := slices.Clone(m.outbounds)
	m.access.Unlock()
	if stage == adapter.StartStateStart {
		return m.startOutbounds(append(outbounds, common.Map(m.endpoint.Endpoints(), func(it adapter.Endpoint) adapter.Outbound { return it })...))
	}
	for _, outbound := range outbounds {
		if err := m.startOutbound(outbound, stage); err != nil {
			return err
		}
	}
	return nil
}

// Each runtime outbound owns a scope that can be released when a provider
// removes it, without retaining every obsolete component until box shutdown.
// lifecycleAccess serializes startup, replacement, removal, and shutdown.
func (m *Manager) startOutbound(outbound adapter.Outbound, stage adapter.StartStage) error {
	outboundScope := m.scopes[outbound]
	if outboundScope == nil {
		if err := m.scope.Context().Err(); err != nil {
			return err
		}
		outboundScope = adapter.NewScope(m.scope.Context(), m.logger)
		m.scopes[outbound] = outboundScope
		if _, scoped := outbound.(adapter.Lifecycle); !scoped {
			if closer, loaded := outbound.(interface{ Close() error }); loaded {
				outboundScope.Add(closer.Close)
			}
		}
	}
	lifecycle, loaded := outbound.(adapter.Lifecycle)
	if !loaded {
		return nil
	}
	name := "outbound/" + outbound.Type() + "[" + outbound.Tag() + "]"
	if err := outboundScope.Start(name, lifecycle, stage); err != nil {
		_ = outboundScope.Close()
		delete(m.scopes, outbound)
		return err
	}
	return nil
}

func (m *Manager) Close() error {
	m.lifecycleAccess.Lock()
	defer m.lifecycleAccess.Unlock()
	m.closed = true
	m.access.Lock()
	m.started = false
	outbounds := m.outbounds
	m.outbounds = nil
	m.access.Unlock()
	var err error
	for _, outbound := range slices.Backward(outbounds) {
		if scope := m.scopes[outbound]; scope != nil {
			err = E.Append(err, scope.Close(), nil)
			delete(m.scopes, outbound)
		}
	}
	return err
}

func (m *Manager) startOutbounds(outbounds []adapter.Outbound) error {
	started := make(map[string]bool)
	for {
		canContinue := false
	startOne:
		for _, outboundToStart := range outbounds {
			outboundTag := outboundToStart.Tag()
			if started[outboundTag] {
				continue
			}
			dependencies := outboundToStart.Dependencies()
			for _, dependency := range dependencies {
				if !started[dependency] {
					continue startOne
				}
			}
			started[outboundTag] = true
			canContinue = true
			// Scoped outbounds also satisfy Endpoint structurally. Only registered
			// endpoints belong to the endpoint manager's lifecycle scope.
			if endpoint, isEndpoint := m.endpoint.Get(outboundTag); isEndpoint && endpoint == outboundToStart {
				err := m.endpoint.StartEndpoint(endpoint)
				if err != nil {
					return err
				}
				continue
			}
			_, isLifecycle := outboundToStart.(adapter.Lifecycle)
			if !isLifecycle {
				continue
			}
			err := m.startOutbound(outboundToStart, adapter.StartStateStart)
			if err != nil {
				return err
			}
		}
		if len(started) == len(outbounds) {
			break
		}
		if canContinue {
			continue
		}
		currentOutbound := common.Find(outbounds, func(it adapter.Outbound) bool {
			return !started[it.Tag()]
		})
		var lintOutbound func(oTree []string, oCurrent adapter.Outbound) error
		lintOutbound = func(oTree []string, oCurrent adapter.Outbound) error {
			problemOutboundTag := common.Find(oCurrent.Dependencies(), func(it string) bool {
				return !started[it]
			})
			if common.Contains(oTree, problemOutboundTag) {
				return E.New("circular outbound dependency: ", strings.Join(oTree, " -> "), " -> ", problemOutboundTag)
			}
			problemOutbound := common.Find(outbounds, func(it adapter.Outbound) bool {
				return it.Tag() == problemOutboundTag
			})
			if problemOutbound == nil {
				return E.New("dependency[", problemOutboundTag, "] not found for outbound[", oCurrent.Tag(), "]")
			}
			return lintOutbound(append(oTree, problemOutboundTag), problemOutbound)
		}
		return lintOutbound([]string{currentOutbound.Tag()}, currentOutbound)
	}
	return nil
}

func (m *Manager) Outbounds() []adapter.Outbound {
	m.access.RLock()
	defer m.access.RUnlock()
	return m.outbounds
}

func (m *Manager) Outbound(tag string) (adapter.Outbound, bool) {
	m.access.RLock()
	outbound, found := m.outboundByTag[tag]
	m.access.RUnlock()
	if found {
		return outbound, true
	}
	return m.endpoint.Get(tag)
}

func (m *Manager) Default() adapter.Outbound {
	m.access.RLock()
	defer m.access.RUnlock()
	return m.defaultOutbound
}

func (m *Manager) Remove(tag string) error {
	m.lifecycleAccess.Lock()
	defer m.lifecycleAccess.Unlock()
	if m.closed {
		return os.ErrClosed
	}
	m.access.Lock()
	outbound, found := m.outboundByTag[tag]
	if !found {
		m.access.Unlock()
		return os.ErrInvalid
	}
	if dependBy := m.dependByTag[tag]; len(dependBy) > 0 {
		m.access.Unlock()
		return E.New("outbound[", tag, "] is depended by ", strings.Join(dependBy, ", "))
	}
	delete(m.outboundByTag, tag)
	index := common.Index(m.outbounds, func(it adapter.Outbound) bool { return it == outbound })
	if index == -1 {
		m.access.Unlock()
		panic("invalid outbound index")
	}
	m.outbounds = slices.Delete(m.outbounds, index, index+1)
	if m.defaultOutbound == outbound {
		m.defaultOutbound = nil
		if len(m.outbounds) > 0 {
			m.defaultOutbound = m.outbounds[0]
		}
	}
	for _, dependency := range outbound.Dependencies() {
		dependBy := common.Filter(m.dependByTag[dependency], func(it string) bool { return it != tag })
		if len(dependBy) == 0 {
			delete(m.dependByTag, dependency)
		} else {
			m.dependByTag[dependency] = dependBy
		}
	}
	m.access.Unlock()
	if scope := m.scopes[outbound]; scope != nil {
		delete(m.scopes, outbound)
		return scope.Close()
	}
	return common.Close(outbound)
}

// Add publishes the stable wrapper used by runtime proxy providers.
func (m *Manager) Add(outbound adapter.Outbound) error {
	m.lifecycleAccess.Lock()
	defer m.lifecycleAccess.Unlock()
	if m.closed {
		return os.ErrClosed
	}
	if m.scope != nil {
		if err := m.scope.Context().Err(); err != nil {
			return err
		}
	}
	tag := outbound.Tag()
	if tag == "" {
		return os.ErrInvalid
	}
	m.access.RLock()
	_, exists := m.outboundByTag[tag]
	started, stage := m.started, m.stage
	m.access.RUnlock()
	if exists {
		return E.New("outbound already exists: ", tag)
	}
	if started {
		for _, startStage := range adapter.ListStartStages {
			if startStage > stage {
				break
			}
			if err := m.startOutbound(outbound, startStage); err != nil {
				return err
			}
		}
	}
	m.access.Lock()
	defer m.access.Unlock()
	m.outbounds = append(m.outbounds, outbound)
	m.outboundByTag[tag] = outbound
	for _, dependency := range outbound.Dependencies() {
		m.dependByTag[dependency] = append(m.dependByTag[dependency], tag)
	}
	if m.defaultTag != "" && tag == m.defaultTag {
		m.defaultOutbound = outbound
	}
	return nil
}

func (m *Manager) UpdateDependencies(tag string, previous []string, current []string) {
	m.access.Lock()
	defer m.access.Unlock()
	for _, dependency := range previous {
		dependBy := common.Filter(m.dependByTag[dependency], func(it string) bool {
			return it != tag
		})
		if len(dependBy) == 0 {
			delete(m.dependByTag, dependency)
		} else {
			m.dependByTag[dependency] = dependBy
		}
	}
	for _, dependency := range current {
		if !common.Contains(m.dependByTag[dependency], tag) {
			m.dependByTag[dependency] = append(m.dependByTag[dependency], tag)
		}
	}
}

func (m *Manager) Create(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, outboundType string, options any) error {
	if tag == "" {
		return os.ErrInvalid
	}
	outbound, err := m.registry.CreateOutbound(ctx, router, logger, tag, outboundType, options)
	if err != nil {
		return err
	}
	m.lifecycleAccess.Lock()
	defer m.lifecycleAccess.Unlock()
	if m.closed {
		_ = common.Close(outbound)
		return os.ErrClosed
	}
	if m.scope != nil {
		if err = m.scope.Context().Err(); err != nil {
			_ = common.Close(outbound)
			return err
		}
	}
	m.access.RLock()
	started, stage := m.started, m.stage
	m.access.RUnlock()
	if started {
		for _, startStage := range adapter.ListStartStages {
			if startStage > stage {
				break
			}
			if err = m.startOutbound(outbound, startStage); err != nil {
				return err
			}
		}
	}
	m.access.Lock()
	previous := m.outboundByTag[tag]
	if previous != nil {
		index := common.Index(m.outbounds, func(it adapter.Outbound) bool { return it == previous })
		if index == -1 {
			m.access.Unlock()
			panic("invalid outbound index")
		}
		m.outbounds = slices.Delete(m.outbounds, index, index+1)
		for _, dependency := range previous.Dependencies() {
			m.dependByTag[dependency] = common.Filter(m.dependByTag[dependency], func(it string) bool { return it != tag })
		}
	}
	m.outbounds = append(m.outbounds, outbound)
	m.outboundByTag[tag] = outbound
	for _, dependency := range outbound.Dependencies() {
		m.dependByTag[dependency] = append(m.dependByTag[dependency], tag)
	}
	if tag == m.defaultTag || (m.defaultTag == "" && m.defaultOutbound == nil) || m.defaultOutbound == previous {
		m.defaultOutbound = outbound
	}
	m.access.Unlock()
	if previousScope := m.scopes[previous]; previousScope != nil {
		delete(m.scopes, previous)
		return previousScope.Close()
	}
	if started && previous != nil {
		return common.Close(previous)
	}
	return nil
}

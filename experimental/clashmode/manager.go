package clashmode

import (
	"context"
	"strings"
	"sync"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/trafficcontrol"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/observable"
	"github.com/sagernet/sing/service"
)

type Manager struct {
	ctx            context.Context
	logger         log.Logger
	dnsRouter      adapter.DNSRouter
	trafficManager allConnectionCloser
	network        networkResetter
	mode           string
	modeList       []string
	updateAccess   sync.Mutex
	updateHooks    []*observable.Subscriber[struct{}]
}

func NewManager(ctx context.Context, logger log.Logger, defaultMode string, modeList []string) *Manager {
	if defaultMode == "" {
		defaultMode = "Rule"
	}
	if !common.Contains(modeList, defaultMode) {
		modeList = append([]string{defaultMode}, modeList...)
	}
	var trafficManager allConnectionCloser
	if manager := service.PtrFromContext[trafficcontrol.Manager](ctx); manager != nil {
		trafficManager = manager
	}
	return &Manager{
		ctx:            ctx,
		logger:         logger,
		dnsRouter:      service.FromContext[adapter.DNSRouter](ctx),
		trafficManager: trafficManager,
		network:        service.FromContext[adapter.NetworkManager](ctx),
		mode:           defaultMode,
		modeList:       modeList,
	}
}

func (m *Manager) Name() string {
	return "clash mode manager"
}

func (m *Manager) Start(stage adapter.StartStage) error {
	if stage != adapter.StartStateStart {
		return nil
	}
	cacheFile := service.FromContext[adapter.CacheFile](m.ctx)
	if cacheFile != nil {
		mode := cacheFile.LoadMode()
		if common.Any(m.modeList, func(it string) bool {
			return strings.EqualFold(it, mode)
		}) {
			m.mode = mode
		}
	}
	return nil
}

func (m *Manager) Close() error {
	return nil
}

func (m *Manager) Mode() string {
	return m.mode
}

func (m *Manager) ModeList() []string {
	return m.modeList
}

func (m *Manager) AddUpdateHook(hook *observable.Subscriber[struct{}]) {
	m.updateAccess.Lock()
	defer m.updateAccess.Unlock()
	m.updateHooks = append(m.updateHooks, hook)
}

func (m *Manager) SetMode(newMode string) {
	if !common.Contains(m.modeList, newMode) {
		newMode = common.Find(m.modeList, func(it string) bool {
			return strings.EqualFold(it, newMode)
		})
	}
	if !common.Contains(m.modeList, newMode) {
		return
	}
	if newMode == m.mode {
		return
	}
	m.mode = newMode
	// Existing connections must follow the newly selected routing mode.
	closeConnectionsForModeSwitch(m.ctx, m.trafficManager, m.network)
	m.updateAccess.Lock()
	for _, hook := range m.updateHooks {
		hook.Emit(struct{}{})
	}
	m.updateAccess.Unlock()
	m.dnsRouter.ClearCache()
	cacheFile := service.FromContext[adapter.CacheFile](m.ctx)
	if cacheFile != nil {
		err := cacheFile.StoreMode(newMode)
		if err != nil {
			m.logger.Error(E.Cause(err, "save mode"))
		}
	}
	m.logger.Info("updated mode: ", newMode)
}

var _ adapter.LifecycleService = (*Manager)(nil)

type allConnectionCloser interface {
	CloseAllConnections()
}

type networkResetter interface {
	ResetNetwork(ctx context.Context)
}

func closeConnectionsForModeSwitch(ctx context.Context, trafficManager allConnectionCloser, network networkResetter) {
	if trafficManager != nil {
		trafficManager.CloseAllConnections()
	}
	if network != nil {
		network.ResetNetwork(ctx)
	}
}

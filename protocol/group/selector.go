package group

import (
	"context"
	"net"
	"slices"
	"sync"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/interrupt"
	"github.com/sagernet/sing-box/common/urltest"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
)

func RegisterSelector(registry *outbound.Registry) {
	outbound.Register[option.SelectorOutboundOptions](registry, C.TypeSelector, NewSelector)
}

var (
	_ adapter.OutboundGroup           = (*Selector)(nil)
	_ adapter.OutboundGroupIcon       = (*Selector)(nil)
	_ adapter.ConnectionHandler       = (*Selector)(nil)
	_ adapter.PacketConnectionHandler = (*Selector)(nil)
)

type Selector struct {
	outbound.Adapter
	ctx                          context.Context
	outbound                     adapter.OutboundManager
	connection                   adapter.ConnectionManager
	logger                       logger.ContextLogger
	access                       sync.RWMutex
	tags                         []string
	icon                         string
	defaultTag                   string
	outbounds                    map[string]adapter.Outbound
	selected                     common.TypedValue[adapter.Outbound]
	history                      *urltest.HistoryStorage
	interruptGroup               *interrupt.Group
	interruptExternalConnections bool
}

func NewSelector(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.SelectorOutboundOptions) (adapter.Outbound, error) {
	outbound := &Selector{
		Adapter:                      outbound.NewAdapter(C.TypeSelector, tag, nil, options.Outbounds),
		ctx:                          ctx,
		outbound:                     service.FromContext[adapter.OutboundManager](ctx),
		connection:                   service.FromContext[adapter.ConnectionManager](ctx),
		logger:                       logger,
		tags:                         options.Outbounds,
		icon:                         options.Icon,
		defaultTag:                   options.Default,
		outbounds:                    make(map[string]adapter.Outbound),
		history:                      service.PtrFromContext[urltest.HistoryStorage](ctx),
		interruptGroup:               interrupt.NewGroup(),
		interruptExternalConnections: options.InterruptExistConnections,
	}
	if len(outbound.tags) == 0 {
		return nil, E.New("missing tags")
	}
	return outbound, nil
}

func (s *Selector) Network() []string {
	selected := s.selected.Load()
	if selected == nil {
		return []string{N.NetworkTCP, N.NetworkUDP}
	}
	return selected.Network()
}

func (s *Selector) Start() error {
	s.access.Lock()
	defer s.access.Unlock()
	for i, tag := range s.tags {
		detour, loaded := s.outbound.Outbound(tag)
		if !loaded {
			return E.New("outbound ", i, " not found: ", tag)
		}
		s.outbounds[tag] = detour
	}

	if s.Tag() != "" {
		cacheFile := service.FromContext[adapter.CacheFile](s.ctx)
		if cacheFile != nil {
			selected := cacheFile.LoadSelected(s.Tag())
			if selected != "" {
				detour, loaded := s.outbounds[selected]
				if loaded {
					s.selected.Store(detour)
					return nil
				}
			}
		}
	}

	if s.defaultTag != "" {
		detour, loaded := s.outbounds[s.defaultTag]
		if !loaded {
			return E.New("default outbound not found: ", s.defaultTag)
		}
		s.selected.Store(detour)
		return nil
	}

	if len(s.tags) > 0 {
		s.selected.Store(s.outbounds[s.tags[0]])
	}
	return nil
}

func (s *Selector) Now() string {
	selected := s.selected.Load()
	if selected == nil {
		s.access.RLock()
		defer s.access.RUnlock()
		if len(s.tags) == 0 {
			return ""
		}
		return s.tags[0]
	}
	return selected.Tag()
}

func (s *Selector) All() []string {
	s.access.RLock()
	defer s.access.RUnlock()
	return slices.Clone(s.tags)
}

func (s *Selector) Dependencies() []string {
	return s.All()
}

func (s *Selector) Icon() string {
	return s.icon
}

func (s *Selector) InterruptsExternalConnections() bool {
	return s.interruptExternalConnections
}

func (s *Selector) SelectOutbound(tag string) bool {
	s.access.RLock()
	detour, loaded := s.outbounds[tag]
	if !loaded {
		s.access.RUnlock()
		return false
	}
	unchanged := s.selected.Swap(detour) == detour
	s.access.RUnlock()
	if unchanged {
		return true
	}
	if s.Tag() != "" {
		cacheFile := service.FromContext[adapter.CacheFile](s.ctx)
		if cacheFile != nil {
			err := cacheFile.StoreSelected(s.Tag(), tag)
			if err != nil {
				s.logger.Error("store selected: ", err)
			}
		}
	}
	s.interruptGroup.Interrupt(s.interruptExternalConnections)
	if s.history != nil {
		s.history.NotifyUpdated()
	}
	return true
}

func (s *Selector) UpdateOutbounds(tags []string) error {
	outbounds := make(map[string]adapter.Outbound, len(tags))
	for i, tag := range tags {
		detour, loaded := s.outbound.Outbound(tag)
		if !loaded {
			return E.New("outbound ", i, " not found: ", tag)
		}
		outbounds[tag] = detour
	}

	s.access.Lock()
	previous := s.selected.Load()
	previousTag := ""
	if previous != nil {
		previousTag = previous.Tag()
	}
	var selected adapter.Outbound
	if previousTag != "" {
		selected = outbounds[previousTag]
	}
	if selected == nil && s.Tag() != "" {
		cacheFile := service.FromContext[adapter.CacheFile](s.ctx)
		if cacheFile != nil {
			selected = outbounds[cacheFile.LoadSelected(s.Tag())]
		}
	}
	if selected == nil && s.defaultTag != "" {
		selected = outbounds[s.defaultTag]
	}
	if selected == nil && len(tags) > 0 {
		selected = outbounds[tags[0]]
	}

	s.tags = slices.Clone(tags)
	s.outbounds = outbounds
	selectionChanged := s.selected.Swap(selected) != selected
	s.access.Unlock()
	if selectionChanged {
		s.interruptGroup.Interrupt(s.interruptExternalConnections)
	}
	if s.history != nil {
		s.history.NotifyUpdated()
	}
	return nil
}

func (s *Selector) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	selected := s.selected.Load()
	if selected == nil {
		return nil, E.New("missing selected outbound")
	}
	conn, err := selected.DialContext(ctx, network, destination)
	if err != nil {
		return nil, err
	}
	return s.interruptGroup.NewConn(conn, interrupt.IsExternalConnectionFromContext(ctx)), nil
}

func (s *Selector) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	selected := s.selected.Load()
	if selected == nil {
		return nil, E.New("missing selected outbound")
	}
	conn, err := selected.ListenPacket(ctx, destination)
	if err != nil {
		return nil, err
	}
	return s.interruptGroup.NewPacketConn(conn, interrupt.IsExternalConnectionFromContext(ctx)), nil
}

func (s *Selector) NewConnection(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	ctx = interrupt.ContextWithIsExternalConnection(ctx)
	selected := s.selected.Load()
	if selected == nil {
		N.CloseOnHandshakeFailure(conn, onClose, E.New("missing selected outbound"))
		return
	}
	conn = s.interruptGroup.NewConn(conn, interrupt.IsExternalConnectionFromContext(ctx))
	if outboundHandler, isHandler := selected.(adapter.ConnectionHandler); isHandler {
		outboundHandler.NewConnection(ctx, conn, metadata, onClose)
	} else {
		s.connection.NewConnection(ctx, selected, conn, metadata, onClose)
	}
}

func (s *Selector) NewPacketConnection(ctx context.Context, conn N.PacketConn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	ctx = interrupt.ContextWithIsExternalConnection(ctx)
	selected := s.selected.Load()
	if selected == nil {
		N.CloseOnHandshakeFailure(conn, onClose, E.New("missing selected outbound"))
		return
	}
	conn = s.interruptGroup.NewNetworkPacketConn(conn, interrupt.IsExternalConnectionFromContext(ctx))
	if outboundHandler, isHandler := selected.(adapter.PacketConnectionHandler); isHandler {
		outboundHandler.NewPacketConnection(ctx, conn, metadata, onClose)
	} else {
		s.connection.NewPacketConnection(ctx, selected, conn, metadata, onClose)
	}
}

func RealTag(detour adapter.Outbound) string {
	if group, isGroup := detour.(adapter.OutboundGroup); isGroup {
		return group.Now()
	}
	return detour.Tag()
}

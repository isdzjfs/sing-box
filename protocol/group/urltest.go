package group

import (
	"context"
	"net"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/interrupt"
	"github.com/sagernet/sing-box/common/urltest"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/batch"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/common/x/list"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/pause"
)

func RegisterURLTest(registry *outbound.Registry) {
	outbound.Register[option.URLTestOutboundOptions](registry, C.TypeURLTest, NewURLTest)
}

var (
	_ adapter.OutboundGroup           = (*URLTest)(nil)
	_ adapter.OutboundGroupIcon       = (*URLTest)(nil)
	_ adapter.InterfaceUpdateListener = (*URLTest)(nil)
)

const maxURLTestDialAttempts = 2

type urlTestOutboundSnapshot struct {
	outbound   adapter.Outbound
	generation uint64
	ctx        context.Context
}

func (s urlTestOutboundSnapshot) dialContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if s.ctx == nil {
		return ctx, func() {}
	}
	dialCtx, cancel := context.WithCancel(ctx)
	go func() {
		select {
		case <-dialCtx.Done():
		case <-s.ctx.Done():
			cancel()
		}
	}()
	return dialCtx, cancel
}

type URLTest struct {
	outbound.Adapter
	ctx                          context.Context
	outbound                     adapter.OutboundManager
	connection                   adapter.ConnectionManager
	logger                       log.ContextLogger
	tagsAccess                   sync.RWMutex
	tags                         []string
	icon                         string
	link                         string
	interval                     time.Duration
	tolerance                    uint16
	idleTimeout                  time.Duration
	group                        *URLTestGroup
	interruptExternalConnections bool
}

func NewURLTest(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.URLTestOutboundOptions) (adapter.Outbound, error) {
	outbound := &URLTest{
		Adapter:                      outbound.NewAdapter(C.TypeURLTest, tag, []string{N.NetworkTCP, N.NetworkUDP}, options.Outbounds),
		ctx:                          ctx,
		outbound:                     service.FromContext[adapter.OutboundManager](ctx),
		connection:                   service.FromContext[adapter.ConnectionManager](ctx),
		logger:                       logger,
		tags:                         options.Outbounds,
		icon:                         options.Icon,
		link:                         options.URL,
		interval:                     time.Duration(options.Interval),
		tolerance:                    options.Tolerance,
		idleTimeout:                  time.Duration(options.IdleTimeout),
		interruptExternalConnections: options.InterruptExistConnections,
	}
	return outbound, nil
}

func (s *URLTest) Start() error {
	s.tagsAccess.RLock()
	tags := slices.Clone(s.tags)
	s.tagsAccess.RUnlock()
	outbounds := make([]adapter.Outbound, 0, len(tags))
	for i, tag := range tags {
		detour, loaded := s.outbound.Outbound(tag)
		if !loaded {
			return E.New("outbound ", i, " not found: ", tag)
		}
		outbounds = append(outbounds, detour)
	}
	group, err := NewURLTestGroup(s.ctx, s.outbound, s.logger, outbounds, s.link, s.interval, s.tolerance, s.idleTimeout, s.interruptExternalConnections)
	if err != nil {
		return err
	}
	s.group = group
	return nil
}

func (s *URLTest) PostStart() error {
	s.group.PostStart()
	return nil
}

func (s *URLTest) Close() error {
	return common.Close(
		common.PtrOrNil(s.group),
	)
}

func (s *URLTest) Now() string {
	if s.group == nil {
		return ""
	}
	if selected := s.group.selectedOutbound(N.NetworkTCP).outbound; selected != nil {
		return selected.Tag()
	}
	if selected := s.group.selectedOutbound(N.NetworkUDP).outbound; selected != nil {
		return selected.Tag()
	}
	return ""
}

func (s *URLTest) All() []string {
	s.tagsAccess.RLock()
	defer s.tagsAccess.RUnlock()
	return slices.Clone(s.tags)
}

func (s *URLTest) Dependencies() []string {
	return s.All()
}

func (s *URLTest) Icon() string {
	return s.icon
}

func (s *URLTest) TestURL() string {
	return s.link
}

func (s *URLTest) InterruptsExternalConnections() bool {
	return s.interruptExternalConnections || s.group != nil && s.group.interruptExternalConnections
}

func (s *URLTest) URLTest(ctx context.Context) (map[string]uint16, error) {
	return s.group.URLTest(ctx)
}

func (s *URLTest) CheckOutbounds() {
	s.group.Touch()
	s.group.CheckOutbounds(true)
}

func (s *URLTest) InterfaceUpdated() {
	group := s.group
	if group == nil {
		return
	}
	if group.pause.IsDevicePaused() || group.pause.IsNetworkPaused() {
		return
	}
	go group.CheckOutbounds(true)
}

func (s *URLTest) UpdateOutbounds(tags []string) error {
	outbounds := make([]adapter.Outbound, 0, len(tags))
	for i, tag := range tags {
		detour, loaded := s.outbound.Outbound(tag)
		if !loaded {
			return E.New("outbound ", i, " not found: ", tag)
		}
		outbounds = append(outbounds, detour)
	}
	s.tagsAccess.Lock()
	s.tags = slices.Clone(tags)
	s.tagsAccess.Unlock()
	if s.group != nil {
		s.group.UpdateOutbounds(outbounds)
	}
	return nil
}

func (s *URLTest) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	s.group.Touch()
	networkName := N.NetworkName(network)
	switch networkName {
	case N.NetworkTCP:
	case N.NetworkUDP:
	default:
		return nil, E.Extend(N.ErrUnknownNetwork, network)
	}

	for attempt := 0; attempt < maxURLTestDialAttempts; attempt++ {
		snapshot := s.group.selectedOutbound(networkName)
		outbound := snapshot.outbound
		if outbound == nil {
			outbound, _ = s.group.Select(networkName)
		}
		if outbound == nil {
			return nil, E.New("missing supported outbound")
		}
		dialCtx, cancel := snapshot.dialContext(ctx)
		conn, err := outbound.DialContext(dialCtx, network, destination)
		cancel()
		if err == nil {
			// The selected outbound can change while the underlying dial is in progress.
			trackedConn, selected := s.group.newSelectedConn(ctx, networkName, outbound, snapshot.generation, conn, interrupt.IsExternalConnectionFromContext(ctx))
			if !selected {
				_ = conn.Close()
				continue
			}
			return trackedConn, nil
		}
		// A selection update can cancel an in-flight dial; retry with the new
		// selection instead of treating the stale outbound as unavailable.
		if !s.group.isSelectedOutbound(networkName, outbound, snapshot.generation) {
			continue
		}
		s.logger.ErrorContext(ctx, err)
		s.group.history.DeleteURLTestHistory(outbound.Tag())
		return nil, err
	}
	return nil, E.New("selected outbound changed while dialing")
}

func (s *URLTest) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	s.group.Touch()

	for attempt := 0; attempt < maxURLTestDialAttempts; attempt++ {
		snapshot := s.group.selectedOutbound(N.NetworkUDP)
		outbound := snapshot.outbound
		if outbound == nil {
			outbound, _ = s.group.Select(N.NetworkUDP)
		}
		if outbound == nil {
			return nil, E.New("missing supported outbound")
		}
		dialCtx, cancel := snapshot.dialContext(ctx)
		conn, err := outbound.ListenPacket(dialCtx, destination)
		cancel()
		if err == nil {
			// The selected outbound can change while the underlying packet dial is in progress.
			trackedConn, selected := s.group.newSelectedPacketConn(ctx, N.NetworkUDP, outbound, snapshot.generation, conn, interrupt.IsExternalConnectionFromContext(ctx))
			if !selected {
				_ = conn.Close()
				continue
			}
			return trackedConn, nil
		}
		// A selection update can cancel an in-flight packet dial; retry with the
		// new selection instead of treating the stale outbound as unavailable.
		if !s.group.isSelectedOutbound(N.NetworkUDP, outbound, snapshot.generation) {
			continue
		}
		s.logger.ErrorContext(ctx, err)
		s.group.history.DeleteURLTestHistory(outbound.Tag())
		return nil, err
	}
	return nil, E.New("selected outbound changed while listening packet")
}

func (s *URLTest) NewConnection(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	ctx = interrupt.ContextWithIsExternalConnection(ctx)
	trackedConn := s.group.newPendingInterruptConn(conn)
	ctx = interrupt.ContextWithConnectionTracker(ctx, trackedConn)
	conn = trackedConn
	s.connection.NewConnection(ctx, s, conn, metadata, onClose)
}

func (s *URLTest) NewPacketConnection(ctx context.Context, conn N.PacketConn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	ctx = interrupt.ContextWithIsExternalConnection(ctx)
	trackedConn := s.group.newPendingInterruptNetworkPacketConn(conn)
	ctx = interrupt.ContextWithConnectionTracker(ctx, trackedConn)
	conn = trackedConn
	s.connection.NewPacketConnection(ctx, s, conn, metadata, onClose)
}

type URLTestGroup struct {
	ctx                          context.Context
	outbound                     adapter.OutboundManager
	pause                        pause.Manager
	pauseCallback                *list.Element[pause.Callback]
	logger                       log.Logger
	membersAccess                sync.RWMutex
	outbounds                    []adapter.Outbound
	link                         string
	interval                     time.Duration
	tolerance                    uint16
	idleTimeout                  time.Duration
	history                      *urltest.HistoryStorage
	checking                     atomic.Bool
	selectedAccess               sync.RWMutex
	selectedOutboundTCP          adapter.Outbound
	selectedOutboundUDP          adapter.Outbound
	tcpGeneration                uint64
	tcpGenerationCtx             context.Context
	tcpGenerationCancel          context.CancelFunc
	udpGeneration                uint64
	udpGenerationCtx             context.Context
	udpGenerationCancel          context.CancelFunc
	connectionGeneration         uint64
	interruptGroup               *interrupt.Group
	interruptExternalConnections bool
	access                       sync.Mutex
	ticker                       *time.Ticker
	close                        chan struct{}
	started                      bool
	lastActive                   common.TypedValue[time.Time]
}

func NewURLTestGroup(ctx context.Context, outboundManager adapter.OutboundManager, logger log.Logger, outbounds []adapter.Outbound, link string, interval time.Duration, tolerance uint16, idleTimeout time.Duration, interruptExternalConnections bool) (*URLTestGroup, error) {
	if interval == 0 {
		interval = C.DefaultURLTestInterval
	}
	if tolerance == 0 {
		tolerance = 50
	}
	if idleTimeout == 0 {
		idleTimeout = C.DefaultURLTestIdleTimeout
	}
	if interval > idleTimeout {
		return nil, E.New("interval must be less or equal than idle_timeout")
	}
	history := service.PtrFromContext[urltest.HistoryStorage](ctx)
	if history == nil {
		return nil, E.New("missing URL test history storage")
	}
	tcpGenerationCtx, tcpGenerationCancel := context.WithCancel(ctx)
	udpGenerationCtx, udpGenerationCancel := context.WithCancel(ctx)
	return &URLTestGroup{
		ctx:                          ctx,
		outbound:                     outboundManager,
		logger:                       logger,
		outbounds:                    outbounds,
		link:                         link,
		interval:                     interval,
		tolerance:                    tolerance,
		idleTimeout:                  idleTimeout,
		history:                      history,
		close:                        make(chan struct{}),
		pause:                        service.FromContext[pause.Manager](ctx),
		tcpGenerationCtx:             tcpGenerationCtx,
		tcpGenerationCancel:          tcpGenerationCancel,
		udpGenerationCtx:             udpGenerationCtx,
		udpGenerationCancel:          udpGenerationCancel,
		interruptGroup:               interrupt.NewGroup(),
		interruptExternalConnections: interruptExternalConnections,
	}, nil
}

func (g *URLTestGroup) PostStart() {
	g.access.Lock()
	defer g.access.Unlock()
	g.started = true
	g.lastActive.Store(time.Now())
	g.startTickerLocked()
	go g.CheckOutbounds(false)
}

func (g *URLTestGroup) Touch() {
	if !g.started {
		return
	}
	g.access.Lock()
	defer g.access.Unlock()
	if g.ticker != nil {
		g.lastActive.Store(time.Now())
		return
	}
	g.startTickerLocked()
}

func (g *URLTestGroup) startTickerLocked() {
	ticker := time.NewTicker(g.interval)
	g.ticker = ticker
	g.pauseCallback = pause.RegisterTicker(g.pause, ticker, g.interval, nil)
	go g.loopCheck(ticker, g.close)
}

func (g *URLTestGroup) Close() error {
	g.selectedAccess.Lock()
	if g.tcpGenerationCancel != nil {
		g.tcpGenerationCancel()
		g.tcpGenerationCancel = nil
	}
	if g.udpGenerationCancel != nil {
		g.udpGenerationCancel()
		g.udpGenerationCancel = nil
	}
	g.selectedAccess.Unlock()
	g.access.Lock()
	defer g.access.Unlock()
	if g.ticker == nil {
		return nil
	}
	g.ticker.Stop()
	g.ticker = nil
	g.pause.UnregisterCallback(g.pauseCallback)
	g.pauseCallback = nil
	close(g.close)
	return nil
}

func (g *URLTestGroup) Select(network string) (adapter.Outbound, bool) {
	outbounds := g.outboundsSnapshot()
	var minDelay uint16
	var minOutbound adapter.Outbound
	switch network {
	case N.NetworkTCP:
		if selected := g.selectedOutbound(network).outbound; selected != nil {
			if history := g.history.LoadURLTestHistory(RealTag(selected)); history != nil {
				minOutbound = selected
				minDelay = history.Delay
			}
		}
	case N.NetworkUDP:
		if selected := g.selectedOutbound(network).outbound; selected != nil {
			if history := g.history.LoadURLTestHistory(RealTag(selected)); history != nil {
				minOutbound = selected
				minDelay = history.Delay
			}
		}
	}
	for _, detour := range outbounds {
		if !common.Contains(detour.Network(), network) {
			continue
		}
		history := g.history.LoadURLTestHistory(RealTag(detour))
		if history == nil {
			continue
		}
		if minDelay == 0 || minDelay > history.Delay+g.tolerance {
			minDelay = history.Delay
			minOutbound = detour
		}
	}
	if minOutbound == nil {
		for _, detour := range outbounds {
			if !common.Contains(detour.Network(), network) {
				continue
			}
			return detour, false
		}
		return nil, false
	}
	return minOutbound, true
}

func (g *URLTestGroup) outboundsSnapshot() []adapter.Outbound {
	g.membersAccess.RLock()
	defer g.membersAccess.RUnlock()
	return slices.Clone(g.outbounds)
}

func (g *URLTestGroup) UpdateOutbounds(outbounds []adapter.Outbound) {
	g.membersAccess.Lock()
	g.outbounds = slices.Clone(outbounds)
	g.membersAccess.Unlock()

	available := make(map[adapter.Outbound]bool, len(outbounds))
	for _, item := range outbounds {
		available[item] = true
	}
	var updated bool
	g.selectedAccess.Lock()
	if g.selectedOutboundTCP != nil && !available[g.selectedOutboundTCP] {
		g.selectedOutboundTCP = nil
		if g.tcpGenerationCancel != nil {
			g.tcpGenerationCancel()
		}
		g.tcpGeneration++
		g.tcpGenerationCtx, g.tcpGenerationCancel = context.WithCancel(g.ctx)
		updated = true
	}
	if g.selectedOutboundUDP != nil && !available[g.selectedOutboundUDP] {
		g.selectedOutboundUDP = nil
		if g.udpGenerationCancel != nil {
			g.udpGenerationCancel()
		}
		g.udpGeneration++
		g.udpGenerationCtx, g.udpGenerationCancel = context.WithCancel(g.ctx)
		updated = true
	}
	if updated {
		g.connectionGeneration++
	}
	generation := g.connectionGeneration
	g.selectedAccess.Unlock()
	if updated {
		g.interruptGroup.InterruptBefore(generation, g.interruptExternalConnections)
	}
	g.performUpdateCheck()
	g.history.NotifyUpdated()
	go g.CheckOutbounds(true)
}

func (g *URLTestGroup) selectedOutbound(network string) urlTestOutboundSnapshot {
	g.selectedAccess.RLock()
	defer g.selectedAccess.RUnlock()
	snapshot := urlTestOutboundSnapshot{outbound: g.selectedOutboundLocked(network)}
	switch network {
	case N.NetworkTCP:
		snapshot.generation = g.tcpGeneration
		snapshot.ctx = g.tcpGenerationCtx
	case N.NetworkUDP:
		snapshot.generation = g.udpGeneration
		snapshot.ctx = g.udpGenerationCtx
	}
	return snapshot
}

func (g *URLTestGroup) selectedOutboundLocked(network string) adapter.Outbound {
	switch network {
	case N.NetworkTCP:
		return g.selectedOutboundTCP
	case N.NetworkUDP:
		return g.selectedOutboundUDP
	default:
		return nil
	}
}

func (g *URLTestGroup) isSelectedOutbound(network string, outbound adapter.Outbound, generation uint64) bool {
	g.selectedAccess.RLock()
	defer g.selectedAccess.RUnlock()
	return g.isSelectedOutboundLocked(network, outbound, generation)
}

func (g *URLTestGroup) isSelectedOutboundLocked(network string, outbound adapter.Outbound, generation uint64) bool {
	var currentGeneration uint64
	switch network {
	case N.NetworkTCP:
		currentGeneration = g.tcpGeneration
	case N.NetworkUDP:
		currentGeneration = g.udpGeneration
	}
	if generation != currentGeneration {
		return false
	}
	selected := g.selectedOutboundLocked(network)
	return selected == nil || selected == outbound
}

func (g *URLTestGroup) newSelectedConn(ctx context.Context, network string, outbound adapter.Outbound, generation uint64, conn net.Conn, isExternal bool) (net.Conn, bool) {
	g.selectedAccess.RLock()
	defer g.selectedAccess.RUnlock()
	if !g.isSelectedOutboundLocked(network, outbound, generation) {
		return nil, false
	}
	interrupt.RegisterConnectionFromContext(ctx, isExternal, g.connectionGeneration)
	return g.interruptGroup.NewConnWithGeneration(conn, isExternal, g.connectionGeneration), true
}

func (g *URLTestGroup) newSelectedPacketConn(ctx context.Context, network string, outbound adapter.Outbound, generation uint64, conn net.PacketConn, isExternal bool) (net.PacketConn, bool) {
	g.selectedAccess.RLock()
	defer g.selectedAccess.RUnlock()
	if !g.isSelectedOutboundLocked(network, outbound, generation) {
		return nil, false
	}
	interrupt.RegisterConnectionFromContext(ctx, isExternal, g.connectionGeneration)
	return g.interruptGroup.NewPacketConnWithGeneration(conn, isExternal, g.connectionGeneration), true
}

func (g *URLTestGroup) newPendingInterruptConn(conn net.Conn) *interrupt.Conn {
	return g.interruptGroup.NewPendingConn(conn)
}

func (g *URLTestGroup) newPendingInterruptNetworkPacketConn(conn N.PacketConn) *interrupt.NetworkPacketConn {
	return g.interruptGroup.NewPendingNetworkPacketConn(conn)
}

func (g *URLTestGroup) loopCheck(ticker *time.Ticker, closeChan <-chan struct{}) {
	if time.Since(g.lastActive.Load()) > g.interval {
		g.lastActive.Store(time.Now())
		g.CheckOutbounds(false)
	}
	for {
		var tickTime time.Time
		select {
		case <-closeChan:
			return
		case tickTime = <-ticker.C:
		}
		if time.Since(g.lastActive.Load()) > g.idleTimeout {
			g.access.Lock()
			if g.ticker == ticker {
				g.ticker.Stop()
				g.ticker = nil
				g.pause.UnregisterCallback(g.pauseCallback)
				g.pauseCallback = nil
			}
			g.access.Unlock()
			return
		}
		g.checkOutboundsAt(false, tickTime)
	}
}

func (g *URLTestGroup) CheckOutbounds(force bool) {
	_, _ = g.urlTest(g.ctx, force, time.Now())
}

func (g *URLTestGroup) checkOutboundsAt(force bool, checkedAt time.Time) {
	_, _ = g.urlTest(g.ctx, force, checkedAt)
}

func (g *URLTestGroup) URLTest(ctx context.Context) (map[string]uint16, error) {
	return g.urlTest(ctx, false, time.Now())
}

func (g *URLTestGroup) urlTest(ctx context.Context, force bool, checkedAt time.Time) (map[string]uint16, error) {
	result := make(map[string]uint16)
	if g.checking.Swap(true) {
		return result, nil
	}
	if checkedAt.IsZero() {
		checkedAt = time.Now()
	}
	defer g.checking.Store(false)
	b, _ := batch.New(ctx, batch.WithConcurrencyNum[any](10))
	checked := make(map[string]bool)
	var resultAccess sync.Mutex
	for _, detour := range g.outboundsSnapshot() {
		tag := detour.Tag()
		realTag := RealTag(detour)
		if checked[realTag] {
			continue
		}
		checked[realTag] = true
		p, loaded := g.outbound.Outbound(realTag)
		if !loaded {
			continue
		}
		if !g.history.ReserveURLTest(realTag, checkedAt, force) {
			continue
		}
		b.Go(realTag, func() (any, error) {
			defer g.history.FinishURLTest(realTag, checkedAt)
			testCtx, cancel := context.WithTimeout(g.ctx, C.TCPTimeout)
			defer cancel()
			t, err := urltest.URLTest(testCtx, g.link, p)
			if err != nil {
				g.logger.Debug("outbound ", tag, " unavailable: ", err)
				g.history.StoreURLTestFailure(realTag, checkedAt)
			} else {
				g.logger.Debug("outbound ", tag, " available: ", t, "ms")
				g.history.StoreURLTestHistory(realTag, &adapter.URLTestHistory{
					Time:  checkedAt,
					Delay: t,
				})
				resultAccess.Lock()
				result[tag] = t
				resultAccess.Unlock()
			}
			return nil, nil
		})
	}
	b.Wait()
	g.performUpdateCheck()
	return result, nil
}

func (g *URLTestGroup) performUpdateCheck() {
	tcpOutbound, tcpExists := g.Select(N.NetworkTCP)
	udpOutbound, udpExists := g.Select(N.NetworkUDP)
	if updated, generation := g.applySelectedUpdate(tcpOutbound, tcpExists, udpOutbound, udpExists); updated {
		g.interruptGroup.InterruptBefore(generation, g.interruptExternalConnections)
	}
}

func (g *URLTestGroup) applySelectedUpdate(tcpOutbound adapter.Outbound, tcpExists bool, udpOutbound adapter.Outbound, udpExists bool) (bool, uint64) {
	g.selectedAccess.Lock()
	defer g.selectedAccess.Unlock()
	var updated bool
	var tcpUpdated bool
	var udpUpdated bool
	if tcpOutbound != nil && (g.selectedOutboundTCP == nil || (tcpExists && tcpOutbound != g.selectedOutboundTCP)) {
		if g.selectedOutboundTCP != tcpOutbound {
			updated = true
			tcpUpdated = true
		}
		g.selectedOutboundTCP = tcpOutbound
	}
	if udpOutbound != nil && (g.selectedOutboundUDP == nil || (udpExists && udpOutbound != g.selectedOutboundUDP)) {
		if g.selectedOutboundUDP != udpOutbound {
			updated = true
			udpUpdated = true
		}
		g.selectedOutboundUDP = udpOutbound
	}
	if updated {
		if tcpUpdated {
			if g.tcpGenerationCancel != nil {
				g.tcpGenerationCancel()
			}
			g.tcpGeneration++
			g.tcpGenerationCtx, g.tcpGenerationCancel = context.WithCancel(g.ctx)
		}
		if udpUpdated {
			if g.udpGenerationCancel != nil {
				g.udpGenerationCancel()
			}
			g.udpGeneration++
			g.udpGenerationCtx, g.udpGenerationCancel = context.WithCancel(g.ctx)
		}
		g.connectionGeneration++
	}
	return updated, g.connectionGeneration
}

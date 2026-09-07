package urltest

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/url"
	"sort"
	"sync"
	"time"

	"github.com/sagernet/sing-anytls"
	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-mux"
	"github.com/sagernet/sing-snell"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/common/ntp"
	"github.com/sagernet/sing/common/observable"
)

type HistoryStorage struct {
	access         sync.RWMutex
	currentHistory map[string]*adapter.URLTestHistory
	delayHistory   map[string][]*adapter.URLTestHistory
	checking       map[string]time.Time
	updateHooks    []*observable.Subscriber[struct{}]
	updateWait     chan struct{}
}

const (
	maxHistoryEntries    = 20
	duplicateCheckWindow = time.Second
)

func NewHistoryStorage() *HistoryStorage {
	return &HistoryStorage{
		currentHistory: make(map[string]*adapter.URLTestHistory),
		delayHistory:   make(map[string][]*adapter.URLTestHistory),
		checking:       make(map[string]time.Time),
		updateWait:     make(chan struct{}),
	}
}

func (s *HistoryStorage) AddUpdateHook(hook *observable.Subscriber[struct{}]) {
	s.access.Lock()
	defer s.access.Unlock()
	s.updateHooks = append(s.updateHooks, hook)
}

func (s *HistoryStorage) NotifyUpdated() {
	s.access.Lock()
	defer s.access.Unlock()
	s.notifyUpdated()
}

func (s *HistoryStorage) LoadURLTestHistory(tag string) *adapter.URLTestHistory {
	if s == nil {
		return nil
	}
	s.access.RLock()
	defer s.access.RUnlock()
	return s.currentHistory[tag]
}

func (s *HistoryStorage) LoadURLTestHistories(tag string) []*adapter.URLTestHistory {
	if s == nil {
		return []*adapter.URLTestHistory{}
	}
	s.access.RLock()
	defer s.access.RUnlock()
	histories := s.delayHistory[tag]
	if len(histories) == 0 {
		return []*adapter.URLTestHistory{}
	}
	return append([]*adapter.URLTestHistory(nil), histories...)
}

func (s *HistoryStorage) appendHistoryLocked(tag string, history *adapter.URLTestHistory) {
	histories := append(s.delayHistory[tag], history)
	sort.SliceStable(histories, func(i, j int) bool {
		return histories[i].Time.Before(histories[j].Time)
	})
	if len(histories) > maxHistoryEntries {
		histories = append([]*adapter.URLTestHistory(nil), histories[len(histories)-maxHistoryEntries:]...)
	}
	s.delayHistory[tag] = histories
}

func (s *HistoryStorage) lastHistoryLocked(tag string) *adapter.URLTestHistory {
	histories := s.delayHistory[tag]
	if len(histories) > 0 {
		return histories[len(histories)-1]
	}
	return s.currentHistory[tag]
}

func (s *HistoryStorage) isLatestHistoryLocked(tag string, checkedAt time.Time) bool {
	latest := s.lastHistoryLocked(tag)
	return latest == nil || !checkedAt.Before(latest.Time)
}

func (s *HistoryStorage) ReserveURLTest(tag string, checkedAt time.Time, force bool) bool {
	if s == nil {
		return true
	}
	if checkedAt.IsZero() {
		checkedAt = time.Now()
	}
	s.access.Lock()
	defer s.access.Unlock()
	if s.checking == nil {
		s.checking = make(map[string]time.Time)
	}
	if _, loaded := s.checking[tag]; loaded {
		return false
	}
	if !force {
		history := s.lastHistoryLocked(tag)
		if history != nil && checkedAt.Sub(history.Time) < duplicateCheckWindow {
			return false
		}
	}
	s.checking[tag] = checkedAt
	return true
}

func (s *HistoryStorage) FinishURLTest(tag string, checkedAt time.Time) {
	if s == nil {
		return
	}
	s.access.Lock()
	defer s.access.Unlock()
	if checkingAt, loaded := s.checking[tag]; loaded && checkingAt.Equal(checkedAt) {
		delete(s.checking, tag)
		s.notifyWaiters()
	}
}

func (s *HistoryStorage) URLTestCheckingAt(tag string) (time.Time, bool) {
	if s == nil {
		return time.Time{}, false
	}
	s.access.RLock()
	defer s.access.RUnlock()
	checkingAt, loaded := s.checking[tag]
	return checkingAt, loaded
}

func (s *HistoryStorage) WaitURLTestResult(ctx context.Context, tag string, checkedAt time.Time) (*adapter.URLTestHistory, error) {
	for {
		s.access.RLock()
		history := s.lastHistoryLocked(tag)
		checkingAt, checking := s.checking[tag]
		updateWait := s.updateWait
		s.access.RUnlock()
		if history != nil && !history.Time.Before(checkedAt) {
			return history, nil
		}
		if !checking || checkingAt.After(checkedAt) {
			return nil, context.Canceled
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-updateWait:
		}
	}
}

func (s *HistoryStorage) DeleteURLTestHistory(tag string) {
	s.access.Lock()
	// Keep visible delay history intact; only remove the current selectable result.
	delete(s.currentHistory, tag)
	s.notifyUpdated()
	s.access.Unlock()
}

func (s *HistoryStorage) StoreURLTestFailure(tag string, checkedAt time.Time) {
	if checkedAt.IsZero() {
		checkedAt = time.Now()
	}
	s.access.Lock()
	history := &adapter.URLTestHistory{
		Time:  checkedAt,
		Delay: 0,
	}
	// Commit the current result by probe time, not completion order, so an older
	// slow probe cannot invalidate a newer result.
	if s.isLatestHistoryLocked(tag, checkedAt) {
		delete(s.currentHistory, tag)
	}
	s.appendHistoryLocked(tag, history)
	s.notifyUpdated()
	s.access.Unlock()
}

func (s *HistoryStorage) StoreURLTestHistory(tag string, history *adapter.URLTestHistory) {
	s.access.Lock()
	isLatest := s.isLatestHistoryLocked(tag, history.Time)
	s.appendHistoryLocked(tag, history)
	if isLatest {
		s.currentHistory[tag] = history
	}
	s.notifyUpdated()
	s.access.Unlock()
}

func (s *HistoryStorage) notifyUpdated() {
	s.notifyWaiters()
	for _, updateHook := range s.updateHooks {
		updateHook.Emit(struct{}{})
	}
}

func (s *HistoryStorage) notifyWaiters() {
	close(s.updateWait)
	s.updateWait = make(chan struct{})
}

func (s *HistoryStorage) Close() error {
	s.access.Lock()
	defer s.access.Unlock()
	s.checking = nil
	s.updateHooks = nil
	s.notifyWaiters()
	return nil
}

func URLTest(ctx context.Context, link string, detour N.Dialer) (uint16, error) {
	multiplexOutbound, isMultiplexOutbound := common.Cast[adapter.OutboundWithMultiplex](detour)
	if isMultiplexOutbound && multiplexOutbound.MultiplexEnabled() {
		warmContext := adapter.ContextWithKeepSession(ctx)
		warmContext = mux.ContextWithKeepSession(warmContext)
		warmContext = anytls.ContextWithKeepSession(warmContext)
		warmContext = contextWithQUICKeepSession(warmContext)
		warmContext = snell.ContextWithKeepSession(warmContext)
		_, err := urlTest(warmContext, link, detour)
		if err != nil {
			return 0, err
		}
	}
	return urlTest(ctx, link, detour)
}

func urlTest(ctx context.Context, link string, detour N.Dialer) (t uint16, err error) {
	if link == "" {
		link = "https://www.gstatic.com/generate_204"
	}
	linkURL, err := url.Parse(link)
	if err != nil {
		return 0, E.Cause(err, "parse URL test target")
	}
	hostname := linkURL.Hostname()
	port := linkURL.Port()
	if port == "" {
		switch linkURL.Scheme {
		case "http":
			port = "80"
		case "https":
			port = "443"
		}
	}

	start := time.Now()
	instance, err := detour.DialContext(ctx, "tcp", M.ParseSocksaddrHostPortStr(hostname, port))
	if err != nil {
		return 0, E.Cause(err, "dial URL test target ", hostname, ":", port)
	}
	defer instance.Close()
	if N.NeedHandshakeForWrite(instance) {
		start = time.Now()
	}
	req, err := http.NewRequest(http.MethodHead, link, nil)
	if err != nil {
		return 0, E.Cause(err, "create URL test request")
	}
	client := http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return instance, nil
			},
			TLSClientConfig: &tls.Config{
				Time:    ntp.TimeFuncFromContext(ctx),
				RootCAs: adapter.RootPoolFromContext(ctx),
			},
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: C.TCPTimeout,
	}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req.WithContext(ctx))
	if err != nil {
		return 0, E.Cause(err, "perform URL test request to ", hostname, ":", port)
	}
	resp.Body.Close()
	t = uint16(time.Since(start) / time.Millisecond)
	return
}

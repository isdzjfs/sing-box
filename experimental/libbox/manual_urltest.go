package libbox

import (
	"context"
	"sync"

	"github.com/sagernet/sing-box/daemon"
	E "github.com/sagernet/sing/common/exceptions"
)

type URLTestItemResult struct {
	ItemTag      string
	Delay        int32
	TestedAt     int64
	ErrorMessage string
	TimedOut     bool
}

type URLTestItemResultIterator interface {
	Next() *URLTestItemResult
	HasNext() bool
}

type URLTestBatchResult struct {
	RequestID string
	results   []*URLTestItemResult
}

func (r *URLTestBatchResult) Results() URLTestItemResultIterator {
	return newIterator(r.results)
}

func (c *CommandClient) URLTestItemsV2(
	requestID string,
	groupTag string,
	itemTags StringIterator,
	testURL string,
	timeoutMillis int64,
) (*URLTestBatchResult, error) {
	response, err := callWithResult(c, func(ctx context.Context, client daemon.StartedServiceClient) (*daemon.URLTestItemsV2Response, error) {
		return client.URLTestItemsV2(ctx, &daemon.URLTestItemsV2Request{
			RequestId:     requestID,
			OutboundTag:   groupTag,
			ItemTags:      iteratorToArray[string](itemTags),
			TestURL:       testURL,
			TimeoutMillis: timeoutMillis,
		})
	})
	if err != nil {
		return nil, E.Cause(err, "url test items v2")
	}
	return urlTestBatchResultFromGRPC(response), nil
}

type ManualURLTestService struct {
	server    *CommandServer
	access    sync.RWMutex
	closed    bool
	closeOnce sync.Once
}

func NewManualURLTestService(platformInterface PlatformInterface) (*ManualURLTestService, error) {
	server, err := NewCommandServer(new(manualURLTestHandler), platformInterface)
	if err != nil {
		return nil, err
	}
	return &ManualURLTestService{server: server}, nil
}

func (s *ManualURLTestService) Start(configContent string) error {
	s.access.RLock()
	defer s.access.RUnlock()
	if s.closed {
		return context.Canceled
	}
	return s.server.StartedService.StartOrReloadService(s.server.ctx, configContent, &daemon.OverrideOptions{})
}

func (s *ManualURLTestService) URLTestItems(
	requestID string,
	groupTag string,
	itemTags StringIterator,
	testURL string,
	timeoutMillis int64,
) (*URLTestBatchResult, error) {
	s.access.RLock()
	if s.closed {
		s.access.RUnlock()
		return nil, context.Canceled
	}
	startedService := s.server.StartedService
	s.access.RUnlock()
	response, err := startedService.URLTestItemsV2(context.Background(), &daemon.URLTestItemsV2Request{
		RequestId:     requestID,
		OutboundTag:   groupTag,
		ItemTags:      iteratorToArray[string](itemTags),
		TestURL:       testURL,
		TimeoutMillis: timeoutMillis,
	})
	if err != nil {
		return nil, E.Cause(err, "manual url test items")
	}
	return urlTestBatchResultFromGRPC(response), nil
}

func (s *ManualURLTestService) Close() {
	s.closeOnce.Do(func() {
		s.access.Lock()
		s.closed = true
		server := s.server
		s.access.Unlock()
		_ = server.CloseService()
		server.Close()
	})
}

func urlTestBatchResultFromGRPC(response *daemon.URLTestItemsV2Response) *URLTestBatchResult {
	results := make([]*URLTestItemResult, 0, len(response.Results))
	for _, item := range response.Results {
		results = append(results, &URLTestItemResult{
			ItemTag:      item.ItemTag,
			Delay:        item.Delay,
			TestedAt:     item.TestedAt,
			ErrorMessage: item.ErrorMessage,
			TimedOut:     item.TimedOut,
		})
	}
	return &URLTestBatchResult{RequestID: response.RequestId, results: results}
}

type manualURLTestHandler struct{}

func (*manualURLTestHandler) ServiceStop() error   { return nil }
func (*manualURLTestHandler) ServiceReload() error { return nil }
func (*manualURLTestHandler) GetSystemProxyStatus() (*SystemProxyStatus, error) {
	return &SystemProxyStatus{}, nil
}
func (*manualURLTestHandler) SetSystemProxyEnabled(bool) error { return nil }
func (*manualURLTestHandler) TriggerNativeCrash() error        { return nil }
func (*manualURLTestHandler) WriteDebugMessage(string)         {}
func (*manualURLTestHandler) ConnectSSHAgent() (int32, error)  { return -1, nil }

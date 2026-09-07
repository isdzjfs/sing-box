package daemon

import (
	"context"
	"errors"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/protocol/group"
	"golang.org/x/sync/singleflight"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	defaultManualURLTestTimeout = 5 * time.Second
	manualURLTestConcurrency    = 10
)

type urlTestScheduler struct {
	ctx             context.Context
	outboundManager adapter.OutboundManager
	historyStorage  *urltest.HistoryStorage
	concurrency     chan struct{}
	inflight        singleflight.Group
}

type urlTestSchedulerResult struct {
	ItemTag  string
	Delay    uint16
	TestedAt time.Time
	Err      error
	TimedOut bool
	Sequence int64
}

func newURLTestScheduler(
	ctx context.Context,
	outboundManager adapter.OutboundManager,
	historyStorage *urltest.HistoryStorage,
	concurrency int,
) *urlTestScheduler {
	if concurrency <= 0 {
		concurrency = 1
	}
	return &urlTestScheduler{
		ctx:             ctx,
		outboundManager: outboundManager,
		historyStorage:  historyStorage,
		concurrency:     make(chan struct{}, concurrency),
	}
}

func (s *urlTestScheduler) Test(
	ctx context.Context,
	groupTag string,
	itemTag string,
	testURL string,
	timeout time.Duration,
) urlTestSchedulerResult {
	if timeout <= 0 || timeout > defaultManualURLTestTimeout {
		timeout = defaultManualURLTestTimeout
	}
	select {
	case <-ctx.Done():
		return urlTestSchedulerResult{ItemTag: itemTag, Err: ctx.Err(), TimedOut: errors.Is(ctx.Err(), context.DeadlineExceeded)}
	default:
	}
	outboundToTest, effectiveTestURL, err := s.resolveTarget(groupTag, itemTag, testURL)
	if err != nil {
		return urlTestSchedulerResult{ItemTag: itemTag, Err: err}
	}
	effectiveTestURL = urltest.NormalizeURL(effectiveTestURL)
	key := itemTag + "\x00" + effectiveTestURL
	resultChannel := s.inflight.DoChan(key, func() (any, error) {
		return s.test(s.ctx, itemTag, effectiveTestURL, outboundToTest, timeout), nil
	})
	select {
	case <-ctx.Done():
		return urlTestSchedulerResult{ItemTag: itemTag, Err: ctx.Err(), TimedOut: errors.Is(ctx.Err(), context.DeadlineExceeded)}
	case result := <-resultChannel:
		if result.Err != nil {
			return urlTestSchedulerResult{ItemTag: itemTag, Err: result.Err}
		}
		return result.Val.(urlTestSchedulerResult)
	}
}

func (s *urlTestScheduler) test(
	requestContext context.Context,
	itemTag string,
	testURL string,
	outboundToTest adapter.Outbound,
	timeout time.Duration,
) urlTestSchedulerResult {
	result := urlTestSchedulerResult{ItemTag: itemTag}
	reservedAt := time.Now()
	if !s.historyStorage.ReserveURLTest(itemTag, reservedAt, true, testURL) {
		checkingAt, checking := s.historyStorage.URLTestCheckingAt(itemTag, testURL)
		if !checking {
			histories := s.historyStorage.LoadURLTestHistories(itemTag, testURL)
			if len(histories) == 0 {
				result.Err = status.Error(codes.Aborted, "url test already completed")
				return result
			}
			applyURLTestHistory(&result, histories[len(histories)-1])
			return result
		}
		waitContext, cancel := context.WithTimeout(requestContext, timeout)
		defer cancel()
		history, waitErr := s.historyStorage.WaitURLTestResult(waitContext, itemTag, checkingAt, testURL)
		if waitErr != nil {
			result.Err = waitErr
			result.TimedOut = errors.Is(waitErr, context.DeadlineExceeded)
			return result
		}
		applyURLTestHistory(&result, history)
		return result
	}
	defer s.historyStorage.FinishURLTest(itemTag, reservedAt, testURL)
	select {
	case s.concurrency <- struct{}{}:
		defer func() { <-s.concurrency }()
	case <-requestContext.Done():
		result.Err = requestContext.Err()
		result.TimedOut = errors.Is(result.Err, context.DeadlineExceeded)
		return result
	}

	checkedAt := time.Now()
	result.TestedAt = checkedAt
	testContext, cancel := context.WithTimeout(s.ctx, timeout)
	defer cancel()
	delay, testErr := urltest.URLTest(testContext, testURL, outboundToTest)
	if testErr != nil {
		result.Sequence = s.historyStorage.StoreURLTestFailure(itemTag, checkedAt, testURL).Sequence
		result.Err = testErr
		result.TimedOut = errors.Is(testErr, context.DeadlineExceeded) || errors.Is(testContext.Err(), context.DeadlineExceeded)
		return result
	}
	result.Delay = delay
	result.Sequence = s.historyStorage.StoreURLTestHistory(itemTag, &adapter.URLTestHistory{Time: checkedAt, Delay: delay}, testURL).Sequence
	return result
}

func (s *urlTestScheduler) resolveTarget(groupTag string, itemTag string, testURL string) (adapter.Outbound, string, error) {
	abstractGroup, loaded := s.outboundManager.Outbound(groupTag)
	if !loaded {
		return nil, "", status.Error(codes.NotFound, "outbound group not found: "+groupTag)
	}
	outboundGroup, isGroup := abstractGroup.(adapter.OutboundGroup)
	if !isGroup {
		return nil, "", status.Error(codes.InvalidArgument, "outbound is not a group: "+groupTag)
	}
	if testURL == "" {
		if urlTestGroup, isURLTest := abstractGroup.(*group.URLTest); isURLTest {
			testURL = urlTestGroup.TestURL()
		}
	}
	outboundToTest, err := outboundInGroupForManager(s.outboundManager, outboundGroup, groupTag, itemTag)
	if err != nil {
		return nil, "", err
	}
	return outboundToTest, testURL, nil
}

func applyURLTestHistory(result *urlTestSchedulerResult, history *adapter.URLTestHistory) {
	result.Sequence = history.Sequence
	result.TestedAt = history.Time
	result.Delay = history.Delay
	if history.Delay == 0 {
		result.Err = status.Error(codes.Unavailable, "url test failed")
	}
}

func outboundInGroupForManager(
	outboundManager adapter.OutboundManager,
	outboundGroup adapter.OutboundGroup,
	groupTag string,
	itemTag string,
) (adapter.Outbound, error) {
	for _, candidateTag := range outboundGroup.All() {
		if candidateTag != itemTag {
			continue
		}
		outboundToTest, loaded := outboundManager.Outbound(candidateTag)
		if !loaded {
			return nil, status.Error(codes.NotFound, "outbound item not found: "+itemTag)
		}
		if _, isGroup := outboundToTest.(adapter.OutboundGroup); isGroup {
			return nil, status.Error(codes.InvalidArgument, "outbound item is a group: "+itemTag)
		}
		return outboundToTest, nil
	}
	return nil, status.Error(codes.NotFound, "outbound item not found in group "+groupTag+": "+itemTag)
}

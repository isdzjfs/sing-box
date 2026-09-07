package daemon

import (
	"context"
	"time"
)

func (s *StartedService) URLTestItemsV2(ctx context.Context, request *URLTestItemsV2Request) (*URLTestItemsV2Response, error) {
	if err := s.waitForStarted(ctx); err != nil {
		return nil, err
	}
	s.serviceAccess.RLock()
	instance := s.instance
	s.serviceAccess.RUnlock()
	if instance == nil {
		return nil, context.Canceled
	}
	scheduler := instance.manualURLTestScheduler()

	timeout := time.Duration(request.TimeoutMillis) * time.Millisecond
	response := &URLTestItemsV2Response{RequestId: request.RequestId}
	seen := make(map[string]bool, len(request.ItemTags))
	itemTags := make([]string, 0, len(request.ItemTags))
	for _, itemTag := range request.ItemTags {
		if itemTag == "" || seen[itemTag] {
			continue
		}
		seen[itemTag] = true
		itemTags = append(itemTags, itemTag)
	}

	type indexedResult struct {
		index  int
		result *URLTestItemV2Result
	}
	type indexedItem struct {
		index   int
		itemTag string
	}
	itemChannel := make(chan indexedItem, len(itemTags))
	resultChannel := make(chan indexedResult, len(itemTags))
	response.Results = make([]*URLTestItemV2Result, len(itemTags))
	for index, itemTag := range itemTags {
		itemChannel <- indexedItem{index: index, itemTag: itemTag}
	}
	close(itemChannel)
	workerCount := len(itemTags)
	if workerCount > manualURLTestConcurrency {
		workerCount = manualURLTestConcurrency
	}
	for range workerCount {
		go func() {
			for item := range itemChannel {
				result := scheduler.Test(ctx, request.OutboundTag, item.itemTag, request.TestURL, timeout)
				itemResult := &URLTestItemV2Result{
					ItemTag:  result.ItemTag,
					Delay:    int32(result.Delay),
					TimedOut: result.TimedOut,
					Sequence: result.Sequence,
				}
				if !result.TestedAt.IsZero() {
					itemResult.TestedAt = result.TestedAt.UnixMilli()
				}
				if result.Err != nil {
					itemResult.ErrorMessage = result.Err.Error()
				}
				resultChannel <- indexedResult{index: item.index, result: itemResult}
			}
		}()
	}
	for range itemTags {
		indexed := <-resultChannel
		response.Results[indexed.index] = indexed.result
	}
	return response, nil
}

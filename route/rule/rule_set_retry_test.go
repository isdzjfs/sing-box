package rule

import (
	"testing"
	"time"
)

// A rule-set that never loaded is empty, so its rules are silently not firing. Waiting out the
// configured interval would leave routing degraded for a day over a transient failure at boot.
func TestNextUpdateDelayRetriesSoonWhenNeverLoaded(t *testing.T) {
	t.Parallel()
	ruleSet := &RemoteRuleSet{updateInterval: 24 * time.Hour}
	if got := ruleSet.nextUpdateDelay(); got != ruleSetRetryInterval {
		t.Errorf("nextUpdateDelay() = %v, want %v", got, ruleSetRetryInterval)
	}
}

// The retry must never push an update further out than the user asked for.
func TestNextUpdateDelayNeverExceedsConfiguredInterval(t *testing.T) {
	t.Parallel()
	ruleSet := &RemoteRuleSet{updateInterval: time.Minute}
	if got := ruleSet.nextUpdateDelay(); got != time.Minute {
		t.Errorf("nextUpdateDelay() = %v, want %v", got, time.Minute)
	}
}

func TestNextUpdateDelayUsesConfiguredIntervalOnceLoaded(t *testing.T) {
	t.Parallel()
	ruleSet := &RemoteRuleSet{updateInterval: 24 * time.Hour, lastUpdated: time.Now()}
	if got := ruleSet.nextUpdateDelay(); got != 24*time.Hour {
		t.Errorf("nextUpdateDelay() = %v, want %v", got, 24*time.Hour)
	}
}

// The mirror list exists because individual edges get blocked, so the budget has to reach the ones
// listed after the first. A fixed per-attempt timeout larger than the average slice would not.
func TestFallbackBudgetCoversEveryMirror(t *testing.T) {
	t.Parallel()
	sources := len(ruleSetMirrorURLs("https://github.com/owner/repo/raw/main/rule.srs")) + 1
	if sources < 2 {
		t.Fatalf("expected several sources to divide the budget across, got %d", sources)
	}
	slice := ruleSetFallbackBudget / time.Duration(sources)
	if slice < ruleSetMinFallbackTimeout {
		t.Errorf(
			"each of the %d sources gets %v, below the %v floor: the budget cannot walk the list",
			sources, slice, ruleSetMinFallbackTimeout,
		)
	}
}

func TestRuleSetCacheKeyIncludesTransportIdentity(t *testing.T) {
	t.Parallel()
	first := ruleSetCacheKey("binary", "https://example.com/rules.srs", "client-a")
	second := ruleSetCacheKey("binary", "https://example.com/rules.srs", "client-b")
	if first == second {
		t.Fatal("the same URL with different HTTP semantics must not share cached content or ETag")
	}
}

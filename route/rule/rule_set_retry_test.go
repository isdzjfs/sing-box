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

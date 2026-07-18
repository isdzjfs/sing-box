package cachefile

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/option"
)

func TestStoreSelectedOption(t *testing.T) {
	defaultCache := New(context.Background(), nil, option.CacheFileOptions{})
	if !defaultCache.storeSelected {
		t.Fatal("selector persistence should remain enabled by default")
	}

	disabled := false
	disabledCache := New(context.Background(), nil, option.CacheFileOptions{StoreSelected: &disabled})
	if disabledCache.storeSelected {
		t.Fatal("selector persistence should be disabled explicitly")
	}
	if selected := disabledCache.LoadSelected("AI"); selected != "" {
		t.Fatalf("unexpected disabled selection: %q", selected)
	}
	if err := disabledCache.StoreSelected("AI", "proxy"); err != nil {
		t.Fatalf("disabled selector persistence should be a no-op: %v", err)
	}
}

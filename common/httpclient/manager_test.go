package httpclient

import (
	"context"
	"reflect"
	"testing"

	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"
)

func TestResolveDirectOptionsSeparatesMirrorAndOriginSemantics(t *testing.T) {
	t.Parallel()
	manager := NewManager(
		context.Background(),
		log.NewNOPFactory().Logger(),
		[]option.HTTPClient{{
			Tag:     "authenticated",
			Version: 1,
			Headers: badoption.HTTPHeader{
				"Authorization": {"Bearer secret"},
			},
			DialerOptions: option.DialerOptions{Detour: "proxy"},
		}},
		"authenticated",
	)

	origin, err := manager.resolveDirectOptions(&option.HTTPClientOptions{Tag: "authenticated"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := origin.Headers.Build().Get("Authorization"); got != "Bearer secret" {
		t.Fatalf("origin direct fallback lost configured headers: %q", got)
	}
	if origin.Version != 1 {
		t.Fatalf("origin direct fallback lost HTTP version: %d", origin.Version)
	}
	if !reflect.DeepEqual(origin.DialerOptions, option.DialerOptions{}) {
		t.Fatalf("origin direct fallback retained routing fields: %+v", origin.DialerOptions)
	}

	mirror, err := manager.resolveDirectOptions(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := mirror.Headers.Build().Get("Authorization"); got != "" {
		t.Fatalf("mirror fallback must not receive origin authorization: %q", got)
	}
}

func TestTransportCacheIdentityIncludesHeaders(t *testing.T) {
	t.Parallel()
	first := option.HTTPClientOptions{
		Headers: badoption.HTTPHeader{"Authorization": {"Bearer first"}},
	}
	second := option.HTTPClientOptions{
		Headers: badoption.HTTPHeader{"Authorization": {"Bearer second"}},
	}
	if transportCacheIdentity(first) == transportCacheIdentity(second) {
		t.Fatal("different request headers must not share a transport cache identity")
	}
}

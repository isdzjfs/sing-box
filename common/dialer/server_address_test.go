package dialer

import (
	"net/netip"
	"testing"
)

func TestServerAddressRecordUsesLatestSuccessfulAddress(t *testing.T) {
	recorder := NewServerAddressRecorder()
	ActivateServerAddressRecorder(recorder)
	t.Cleanup(func() {
		DeactivateServerAddressRecorder(recorder)
	})
	recorder.Record("proxy-a", "Example.COM.", 443, netip.MustParseAddr("2001:db8::1"))
	recorder.Record("proxy-a", "example.com", 443, netip.MustParseAddr("203.0.113.8"))

	address, updatedAt, loaded := LastServerAddress("proxy-a", "EXAMPLE.COM.", 443)
	if !loaded {
		t.Fatal("expected server address record")
	}
	if address != netip.MustParseAddr("203.0.113.8") {
		t.Fatalf("unexpected address: %s", address)
	}
	if updatedAt <= 0 {
		t.Fatalf("unexpected update time: %d", updatedAt)
	}
}

func TestServerAddressRecordRequiresValidDomainAndPort(t *testing.T) {
	recorder := NewServerAddressRecorder()
	ActivateServerAddressRecorder(recorder)
	t.Cleanup(func() {
		DeactivateServerAddressRecorder(recorder)
	})
	recorder.Record("", "example.invalid", 443, netip.MustParseAddr("203.0.113.8"))
	recorder.Record("proxy-a", "", 443, netip.MustParseAddr("203.0.113.8"))
	recorder.Record("proxy-a", "example.invalid", 0, netip.MustParseAddr("203.0.113.8"))
	recorder.Record("proxy-a", "example.invalid", 443, netip.Addr{})

	if _, _, loaded := LastServerAddress("", "example.invalid", 443); loaded {
		t.Fatal("empty outbound tag must not be recorded")
	}
	if _, _, loaded := LastServerAddress("proxy-a", "", 443); loaded {
		t.Fatal("empty server must not be recorded")
	}
	if _, _, loaded := LastServerAddress("proxy-a", "example.invalid", 0); loaded {
		t.Fatal("zero port must not be recorded")
	}
	if _, _, loaded := LastServerAddress("proxy-a", "example.invalid", 443); loaded {
		t.Fatal("invalid address must not be recorded")
	}
}

func TestServerAddressRecordSeparatesOutboundTags(t *testing.T) {
	recorder := NewServerAddressRecorder()
	ActivateServerAddressRecorder(recorder)
	t.Cleanup(func() {
		DeactivateServerAddressRecorder(recorder)
	})
	recorder.Record("proxy-a", "example.com", 443, netip.MustParseAddr("203.0.113.8"))
	recorder.Record("proxy-b", "example.com", 443, netip.MustParseAddr("203.0.113.9"))

	addressA, _, loadedA := LastServerAddress("proxy-a", "example.com", 443)
	addressB, _, loadedB := LastServerAddress("proxy-b", "example.com", 443)
	if !loadedA || addressA != netip.MustParseAddr("203.0.113.8") {
		t.Fatalf("unexpected proxy-a address: %s", addressA)
	}
	if !loadedB || addressB != netip.MustParseAddr("203.0.113.9") {
		t.Fatalf("unexpected proxy-b address: %s", addressB)
	}
}

func TestServerAddressRecordIsolatedByActiveInstance(t *testing.T) {
	oldRecorder := NewServerAddressRecorder()
	newRecorder := NewServerAddressRecorder()
	ActivateServerAddressRecorder(oldRecorder)
	oldRecorder.Record("proxy-a", "example.com", 443, netip.MustParseAddr("203.0.113.8"))

	ActivateServerAddressRecorder(newRecorder)
	oldRecorder.Record("proxy-a", "example.com", 443, netip.MustParseAddr("203.0.113.9"))
	newRecorder.Record("proxy-a", "example.com", 443, netip.MustParseAddr("203.0.113.10"))
	DeactivateServerAddressRecorder(oldRecorder)

	address, _, loaded := LastServerAddress("proxy-a", "example.com", 443)
	if !loaded || address != netip.MustParseAddr("203.0.113.10") {
		t.Fatalf("unexpected active instance address: %s", address)
	}

	DeactivateServerAddressRecorder(newRecorder)
	if _, _, loaded = LastServerAddress("proxy-a", "example.com", 443); loaded {
		t.Fatal("closed active instance must not expose stale records")
	}
}

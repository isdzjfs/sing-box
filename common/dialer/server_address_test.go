package dialer

import (
	"net/netip"
	"testing"
)

func TestServerAddressRecordUsesLatestSuccessfulAddress(t *testing.T) {
	RecordServerAddress("Example.COM.", 443, netip.MustParseAddr("2001:db8::1"))
	RecordServerAddress("example.com", 443, netip.MustParseAddr("203.0.113.8"))

	address, updatedAt, loaded := LastServerAddress("EXAMPLE.COM.", 443)
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
	RecordServerAddress("", 443, netip.MustParseAddr("203.0.113.8"))
	RecordServerAddress("example.invalid", 0, netip.MustParseAddr("203.0.113.8"))
	RecordServerAddress("example.invalid", 443, netip.Addr{})

	if _, _, loaded := LastServerAddress("", 443); loaded {
		t.Fatal("empty server must not be recorded")
	}
	if _, _, loaded := LastServerAddress("example.invalid", 0); loaded {
		t.Fatal("zero port must not be recorded")
	}
	if _, _, loaded := LastServerAddress("example.invalid", 443); loaded {
		t.Fatal("invalid address must not be recorded")
	}
}

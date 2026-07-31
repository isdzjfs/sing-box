package dialer

import (
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type serverAddressRecord struct {
	address   netip.Addr
	updatedAt int64
}

type serverAddressKey struct {
	outboundTag string
	server      string
	port        uint16
}

// ServerAddressRecorder stores successful server addresses for one core instance.
type ServerAddressRecorder struct {
	records sync.Map
}

var activeServerAddressRecorder atomic.Pointer[ServerAddressRecorder]

// NewServerAddressRecorder creates an empty recorder for a core instance.
func NewServerAddressRecorder() *ServerAddressRecorder {
	return new(ServerAddressRecorder)
}

// ActivateServerAddressRecorder exposes records from the current core instance.
func ActivateServerAddressRecorder(recorder *ServerAddressRecorder) {
	activeServerAddressRecorder.Store(recorder)
}

// DeactivateServerAddressRecorder hides recorder only if it is still active.
func DeactivateServerAddressRecorder(recorder *ServerAddressRecorder) {
	activeServerAddressRecorder.CompareAndSwap(recorder, nil)
}

// Record stores the address selected for an outbound server after a connection succeeds.
func (r *ServerAddressRecorder) Record(outboundTag string, server string, port uint16, address netip.Addr) {
	if r == nil {
		return
	}
	server = normalizeServerAddressKey(server)
	if strings.TrimSpace(outboundTag) == "" || server == "" || port == 0 || !address.IsValid() {
		return
	}
	r.records.Store(serverAddressKey{
		outboundTag: outboundTag,
		server:      server,
		port:        port,
	}, serverAddressRecord{
		address:   address,
		updatedAt: time.Now().UnixMilli(),
	})
}

// LastServerAddress returns a record only from the active core instance.
func LastServerAddress(outboundTag string, server string, port uint16) (netip.Addr, int64, bool) {
	recorder := activeServerAddressRecorder.Load()
	if recorder == nil {
		return netip.Addr{}, 0, false
	}
	return recorder.last(outboundTag, server, port)
}

func (r *ServerAddressRecorder) last(outboundTag string, server string, port uint16) (netip.Addr, int64, bool) {
	server = normalizeServerAddressKey(server)
	if strings.TrimSpace(outboundTag) == "" || server == "" || port == 0 {
		return netip.Addr{}, 0, false
	}
	record, loaded := r.records.Load(serverAddressKey{
		outboundTag: outboundTag,
		server:      server,
		port:        port,
	})
	if !loaded {
		return netip.Addr{}, 0, false
	}
	value, ok := record.(serverAddressRecord)
	if !ok || !value.address.IsValid() || value.updatedAt <= 0 {
		return netip.Addr{}, 0, false
	}
	return value.address, value.updatedAt, true
}

func normalizeServerAddressKey(server string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(server), "."))
}

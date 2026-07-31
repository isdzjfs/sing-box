package dialer

import (
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
)

type serverAddressRecord struct {
	address   netip.Addr
	updatedAt int64
}

var serverAddressRecords sync.Map

// RecordServerAddress stores the address selected for a domain server after a
// connection succeeds. The record intentionally lives in memory only: an
// address selected by a previous core process must not be presented as current.
func RecordServerAddress(server string, port uint16, address netip.Addr) {
	server = normalizeServerAddressKey(server)
	if server == "" || port == 0 || !address.IsValid() {
		return
	}
	serverAddressRecords.Store(serverAddressKey(server, port), serverAddressRecord{
		address:   address,
		updatedAt: time.Now().UnixMilli(),
	})
}

func LastServerAddress(server string, port uint16) (netip.Addr, int64, bool) {
	server = normalizeServerAddressKey(server)
	if server == "" || port == 0 {
		return netip.Addr{}, 0, false
	}
	record, loaded := serverAddressRecords.Load(serverAddressKey(server, port))
	if !loaded {
		return netip.Addr{}, 0, false
	}
	value, ok := record.(serverAddressRecord)
	if !ok || !value.address.IsValid() || value.updatedAt <= 0 {
		return netip.Addr{}, 0, false
	}
	return value.address, value.updatedAt, true
}

func ClearServerAddressRecords() {
	serverAddressRecords.Range(func(key, _ any) bool {
		serverAddressRecords.Delete(key)
		return true
	})
}

func serverAddressKey(server string, port uint16) string {
	return server + ":" + strconv.FormatUint(uint64(port), 10)
}

func normalizeServerAddressKey(server string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(server), "."))
}

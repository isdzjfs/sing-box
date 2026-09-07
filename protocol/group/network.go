package group

import (
	"slices"

	"github.com/sagernet/sing-box/adapter"
	N "github.com/sagernet/sing/common/network"
)

func supportsNetwork(manager adapter.OutboundManager, detour adapter.Outbound, network string, visited map[adapter.Outbound]bool) bool {
	if detour == nil || visited[detour] {
		return false
	}
	visited[detour] = true
	defer delete(visited, detour)
	switch nested := detour.(type) {
	case *URLTest:
		if nested.group == nil {
			return false
		}
		for _, member := range nested.group.outboundsSnapshot() {
			if supportsNetwork(nested.outbound, member, network, visited) {
				return true
			}
		}
		return false
	case *Selector:
		return supportsNetwork(nested.outbound, nested.selected.Load(), network, visited)
	default:
		return slices.Contains(detour.Network(), network)
	}
}

// A nested URLTEST owns its probe URL; a selector inherits its parent's target.
// Resolve the selected leaf for the requested network, including cold-start fallback.
func URLTestHistoryScope(manager adapter.OutboundManager, detour adapter.Outbound, network string, link string) (string, string) {
	visited := make(map[string]bool)
	for detour != nil {
		tag := detour.Tag()
		if visited[tag] {
			return "", link
		}
		visited[tag] = true
		if nested, loaded := detour.(*URLTest); loaded {
			link = nested.TestURL()
		}
		nested, loaded := detour.(adapter.OutboundGroup)
		if !loaded {
			return tag, link
		}
		tag = adapter.OutboundGroupNow(nested, network)
		if manager == nil {
			return tag, link
		}
		detour, loaded = manager.Outbound(tag)
		if !loaded {
			return tag, link
		}
	}
	return "", link
}

func (s *URLTest) Network() []string {
	var networks []string
	for _, network := range []string{N.NetworkTCP, N.NetworkUDP} {
		if supportsNetwork(s.outbound, s, network, make(map[adapter.Outbound]bool)) {
			networks = append(networks, network)
		}
	}
	return networks
}

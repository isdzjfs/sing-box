package adapter

// OutboundGroupForNetwork exposes a display/dial fallback without committing a
// selection or advancing the connection interruption generation.
type OutboundGroupForNetwork interface {
	NowForNetwork(network string) string
}

func OutboundGroupNow(group OutboundGroup, network string) string {
	if networkGroup, loaded := group.(OutboundGroupForNetwork); loaded {
		return networkGroup.NowForNetwork(network)
	}
	return group.Now()
}

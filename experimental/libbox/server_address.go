package libbox

import "github.com/sagernet/sing-box/common/dialer"

// ServerAddressInfo describes the most recent successful address selected for
// a domain server by the core process.
type ServerAddressInfo struct {
	Address   string
	UpdatedAt int64
}

func GetServerAddressInfo(server string, port int32) *ServerAddressInfo {
	if port <= 0 || port > 65535 {
		return nil
	}
	address, updatedAt, loaded := dialer.LastServerAddress(server, uint16(port))
	if !loaded {
		return nil
	}
	return &ServerAddressInfo{
		Address:   address.String(),
		UpdatedAt: updatedAt,
	}
}

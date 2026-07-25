package option

import "github.com/sagernet/sing/common/json/badoption"

type SelectorOutboundOptions struct {
	Outbounds                 []string `json:"outbounds" reference:"outbound"`
	Use                       []string `json:"use,omitempty"`
	Filter                    string   `json:"filter,omitempty"`
	ExcludeFilter             string   `json:"exclude-filter,omitempty"`
	ExcludeType               string   `json:"exclude-type,omitempty"`
	Icon                      string   `json:"icon,omitempty"`
	Default                   string   `json:"default,omitempty" reference:"outbound"`
	InterruptExistConnections bool     `json:"interrupt_exist_connections,omitempty"`
}

type URLTestOutboundOptions struct {
	Outbounds                 []string           `json:"outbounds" reference:"outbound"`
	Use                       []string           `json:"use,omitempty"`
	Filter                    string             `json:"filter,omitempty"`
	ExcludeFilter             string             `json:"exclude-filter,omitempty"`
	ExcludeType               string             `json:"exclude-type,omitempty"`
	Icon                      string             `json:"icon,omitempty"`
	URL                       string             `json:"url,omitempty"`
	Interval                  badoption.Duration `json:"interval,omitempty"`
	Tolerance                 uint16             `json:"tolerance,omitempty"`
	IdleTimeout               badoption.Duration `json:"idle_timeout,omitempty"`
	InterruptExistConnections bool               `json:"interrupt_exist_connections,omitempty"`
}

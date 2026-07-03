package option

import "github.com/sagernet/sing/common/json/badoption"

type ProxyProviderMap map[string]ProxyProvider

type ProxyProvider struct {
	Type          string                `json:"type,omitempty"`
	URL           string                `json:"url,omitempty"`
	Path          string                `json:"path,omitempty"`
	Proxy         string                `json:"proxy,omitempty"`
	Interval      int                   `json:"interval,omitempty"`
	Filter        string                `json:"filter,omitempty"`
	ExcludeFilter string                `json:"exclude-filter,omitempty"`
	ExcludeType   string                `json:"exclude-type,omitempty"`
	Header        badoption.HTTPHeader  `json:"header,omitempty"`
	Override      ProxyProviderOverride `json:"override,omitempty"`
}

type ProxyProviderOverride struct {
	UDP              *bool  `json:"udp,omitempty"`
	IPVersion        string `json:"ip-version,omitempty"`
	Insecure         *bool  `json:"insecure,omitempty"`
	AdditionalPrefix string `json:"additional-prefix,omitempty"`
}

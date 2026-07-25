package option

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/schema"
	E "github.com/sagernet/sing/common/exceptions"
	sing_json "github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/common/json/badjson"
	"github.com/sagernet/sing/common/json/badoption"
)

type _V2RayTransportOptions struct {
	Type               string                  `json:"type" enum:"http,ws,quic,grpc,httpupgrade,xhttp,splithttp"`
	HTTPOptions        V2RayHTTPOptions        `json:"-"`
	WebsocketOptions   V2RayWebsocketOptions   `json:"-"`
	QUICOptions        V2RayQUICOptions        `json:"-"`
	GRPCOptions        V2RayGRPCOptions        `json:"-"`
	HTTPUpgradeOptions V2RayHTTPUpgradeOptions `json:"-"`
	XHTTPOptions       V2RayXHTTPOptions       `json:"-"`
}

type V2RayTransportOptions _V2RayTransportOptions

func (o V2RayTransportOptions) MarshalJSON() ([]byte, error) {
	var v any
	switch o.Type {
	case C.V2RayTransportTypeHTTP:
		v = o.HTTPOptions
	case C.V2RayTransportTypeWebsocket:
		v = o.WebsocketOptions
	case C.V2RayTransportTypeQUIC:
		v = o.QUICOptions
	case C.V2RayTransportTypeGRPC:
		v = o.GRPCOptions
	case C.V2RayTransportTypeHTTPUpgrade:
		v = o.HTTPUpgradeOptions
	case C.V2RayTransportTypeXHTTP, C.V2RayTransportTypeSplitHTTP:
		v = o.XHTTPOptions
	case "":
		return nil, E.New("missing transport type")
	default:
		return nil, E.New("unknown transport type: " + o.Type)
	}
	return badjson.MarshallObjects((_V2RayTransportOptions)(o), v)
}

func (o *V2RayTransportOptions) UnmarshalJSON(bytes []byte) error {
	err := sing_json.Unmarshal(bytes, (*_V2RayTransportOptions)(o))
	if err != nil {
		return err
	}
	var v any
	switch o.Type {
	case C.V2RayTransportTypeHTTP:
		v = &o.HTTPOptions
	case C.V2RayTransportTypeWebsocket:
		v = &o.WebsocketOptions
	case C.V2RayTransportTypeQUIC:
		v = &o.QUICOptions
	case C.V2RayTransportTypeGRPC:
		v = &o.GRPCOptions
	case C.V2RayTransportTypeHTTPUpgrade:
		v = &o.HTTPUpgradeOptions
	case C.V2RayTransportTypeXHTTP, C.V2RayTransportTypeSplitHTTP:
		v = &o.XHTTPOptions
	default:
		return E.New("unknown transport type: " + o.Type)
	}
	err = badjson.UnmarshallExcluded(bytes, (*_V2RayTransportOptions)(o), v)
	if err != nil {
		return err
	}
	return nil
}

func (o V2RayTransportOptions) DescribeSchema(builder schema.Builder) (*schema.Node, error) {
	return builder.Define("V2RayTransport", func() (*schema.Node, error) {
		return schema.DiscriminatedUnion(builder, "type", true, []schema.UnionVariant{
			{Value: C.V2RayTransportTypeHTTP, StructType: reflect.TypeFor[V2RayHTTPOptions]()},
			{Value: C.V2RayTransportTypeWebsocket, StructType: reflect.TypeFor[V2RayWebsocketOptions]()},
			{Value: C.V2RayTransportTypeQUIC, StructType: reflect.TypeFor[V2RayQUICOptions]()},
			{Value: C.V2RayTransportTypeGRPC, StructType: reflect.TypeFor[V2RayGRPCOptions]()},
			{Value: C.V2RayTransportTypeHTTPUpgrade, StructType: reflect.TypeFor[V2RayHTTPUpgradeOptions]()},
			{Value: C.V2RayTransportTypeXHTTP, StructType: reflect.TypeFor[V2RayXHTTPOptions]()},
			{Value: C.V2RayTransportTypeSplitHTTP, StructType: reflect.TypeFor[V2RayXHTTPOptions]()},
		}, nil)
	})
}

type V2RayHTTPOptions struct {
	Host        badoption.Listable[string] `json:"host,omitempty"`
	Path        string                     `json:"path,omitempty"`
	Method      string                     `json:"method,omitempty"`
	Headers     badoption.HTTPHeader       `json:"headers,omitempty"`
	IdleTimeout badoption.Duration         `json:"idle_timeout,omitempty"`
	PingTimeout badoption.Duration         `json:"ping_timeout,omitempty"`
}

type V2RayWebsocketOptions struct {
	Path                string               `json:"path,omitempty"`
	Headers             badoption.HTTPHeader `json:"headers,omitempty"`
	MaxEarlyData        uint32               `json:"max_early_data,omitempty"`
	EarlyDataHeaderName string               `json:"early_data_header_name,omitempty"`
}

type V2RayQUICOptions struct{}

type V2RayGRPCOptions struct {
	ServiceName         string             `json:"service_name,omitempty"`
	IdleTimeout         badoption.Duration `json:"idle_timeout,omitempty"`
	PingTimeout         badoption.Duration `json:"ping_timeout,omitempty"`
	PermitWithoutStream bool               `json:"permit_without_stream,omitempty"`
	ForceLite           bool               `json:"-"` // for test
}

type V2RayHTTPUpgradeOptions struct {
	Host    string               `json:"host,omitempty"`
	Path    string               `json:"path,omitempty"`
	Headers badoption.HTTPHeader `json:"headers,omitempty"`
}

type V2RayXHTTPRangeOptions struct {
	From int32 `json:"from,omitempty"`
	To   int32 `json:"to,omitempty"`
}

func (o *V2RayXHTTPRangeOptions) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		return nil
	}
	switch data[0] {
	case '"':
		var rangeValue string
		if err := json.Unmarshal(data, &rangeValue); err != nil {
			return err
		}
		from, to, err := parseV2RayXHTTPRange(rangeValue)
		if err != nil {
			return err
		}
		o.From = from
		o.To = to
		return nil
	case '[':
		var values []int32
		if err := json.Unmarshal(data, &values); err != nil {
			return err
		}
		switch len(values) {
		case 1:
			o.From = values[0]
			o.To = values[0]
		case 2:
			o.From = values[0]
			o.To = values[1]
		default:
			return E.New("invalid xhttp range length")
		}
		return nil
	default:
		var object struct {
			From int32 `json:"from"`
			To   int32 `json:"to"`
		}
		if err := json.Unmarshal(data, &object); err == nil {
			o.From = object.From
			o.To = object.To
			return nil
		}
		var fixed int32
		if err := json.Unmarshal(data, &fixed); err != nil {
			return err
		}
		o.From = fixed
		o.To = fixed
		return nil
	}
}

func (o V2RayXHTTPRangeOptions) DescribeSchema(_ schema.Builder) (*schema.Node, error) {
	objectForm := schema.StrictObject()
	objectForm.Properties.Put("from", schema.IntegerNode())
	objectForm.Properties.Put("to", schema.IntegerNode())
	return schema.AnyOf(
		schema.StringNode(),
		&schema.Node{Type: "array", Items: schema.IntegerNode()},
		objectForm,
		schema.IntegerNode(),
	), nil
}

func parseV2RayXHTTPRange(rangeValue string) (int32, int32, error) {
	rangeValue = strings.TrimSpace(rangeValue)
	if rangeValue == "" {
		return 0, 0, nil
	}
	parts := strings.SplitN(rangeValue, "-", 2)
	from64, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 32)
	if err != nil {
		return 0, 0, err
	}
	if len(parts) == 1 {
		return int32(from64), int32(from64), nil
	}
	to64, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 32)
	if err != nil {
		return 0, 0, err
	}
	return int32(from64), int32(to64), nil
}

type V2RayXHTTPOptions struct {
	Host                 string                     `json:"host,omitempty"`
	Path                 string                     `json:"path,omitempty"`
	Mode                 string                     `json:"mode,omitempty"`
	Headers              badoption.HTTPHeader       `json:"headers,omitempty"`
	XPaddingBytes        *V2RayXHTTPRangeOptions    `json:"x_padding_bytes,omitempty"`
	XPaddingObfsMode     bool                       `json:"x_padding_obfs_mode,omitempty"`
	XPaddingKey          string                     `json:"x_padding_key,omitempty"`
	XPaddingHeader       string                     `json:"x_padding_header,omitempty"`
	XPaddingPlacement    string                     `json:"x_padding_placement,omitempty"`
	XPaddingMethod       string                     `json:"x_padding_method,omitempty"`
	UplinkHTTPMethod     string                     `json:"uplink_http_method,omitempty"`
	SessionIDPlacement   string                     `json:"session_id_placement,omitempty"`
	SessionIDKey         string                     `json:"session_id_key,omitempty"`
	SessionIDTable       string                     `json:"session_id_table,omitempty"`
	SessionIDLength      *V2RayXHTTPRangeOptions    `json:"session_id_length,omitempty"`
	SeqPlacement         string                     `json:"seq_placement,omitempty"`
	SeqKey               string                     `json:"seq_key,omitempty"`
	UplinkDataPlacement  string                     `json:"uplink_data_placement,omitempty"`
	UplinkDataKey        string                     `json:"uplink_data_key,omitempty"`
	UplinkChunkSize      *V2RayXHTTPRangeOptions    `json:"uplink_chunk_size,omitempty"`
	NoGRPCHeader         bool                       `json:"no_grpc_header,omitempty"`
	NoSSEHeader          bool                       `json:"no_sse_header,omitempty"`
	ScMaxEachPostBytes   *V2RayXHTTPRangeOptions    `json:"sc_max_each_post_bytes,omitempty"`
	ScMinPostsIntervalMs *V2RayXHTTPRangeOptions    `json:"sc_min_posts_interval_ms,omitempty"`
	ScMaxBufferedPosts   int                        `json:"sc_max_buffered_posts,omitempty"`
	ScStreamUpServerSecs *V2RayXHTTPRangeOptions    `json:"sc_stream_up_server_secs,omitempty"`
	ServerMaxHeaderBytes int                        `json:"server_max_header_bytes,omitempty"`
	Xmux                 *V2RayXHTTPXMuxOptions     `json:"xmux,omitempty"`
	DownloadSettings     *V2RayXHTTPDownloadOptions `json:"download_settings,omitempty"`
}

func (o V2RayXHTTPOptions) DescribeSchema(builder schema.Builder) (*schema.Node, error) {
	return builder.Define("V2RayXHTTPOptions", func() (*schema.Node, error) {
		objectForm := schema.StrictObject()
		if err := builder.FlattenStruct(objectForm, reflect.TypeFor[V2RayXHTTPOptions]()); err != nil {
			return nil, err
		}
		return objectForm, nil
	})
}

type V2RayXHTTPXMuxOptions struct {
	MaxConcurrency   *V2RayXHTTPRangeOptions `json:"max_concurrency,omitempty"`
	MaxConnections   *V2RayXHTTPRangeOptions `json:"max_connections,omitempty"`
	CMaxReuseTimes   *V2RayXHTTPRangeOptions `json:"c_max_reuse_times,omitempty"`
	HMaxRequestTimes *V2RayXHTTPRangeOptions `json:"h_max_request_times,omitempty"`
	HMaxReusableSecs *V2RayXHTTPRangeOptions `json:"h_max_reusable_secs,omitempty"`
	HKeepAlivePeriod int64                   `json:"h_keep_alive_period,omitempty"`
}

func (o V2RayXHTTPXMuxOptions) DescribeSchema(builder schema.Builder) (*schema.Node, error) {
	objectForm := schema.StrictObject()
	if err := builder.FlattenStruct(objectForm, reflect.TypeFor[V2RayXHTTPXMuxOptions]()); err != nil {
		return nil, err
	}
	return objectForm, nil
}

type V2RayXHTTPDownloadOptions struct {
	ServerOptions
	DialerOptions
	TLS       *OutboundTLSOptions    `json:"tls,omitempty"`
	Transport *V2RayTransportOptions `json:"transport,omitempty"`

	Address           string                          `json:"address,omitempty"`
	Port              uint16                          `json:"port,omitempty"`
	Network           string                          `json:"network,omitempty"`
	Security          string                          `json:"security,omitempty"`
	TLSSettings       *V2RayXHTTPTLSCompatOptions     `json:"tlsSettings,omitempty"`
	RealitySettings   *V2RayXHTTPRealityCompatOptions `json:"realitySettings,omitempty"`
	XHTTPSettings     *V2RayXHTTPOptions              `json:"xhttpSettings,omitempty"`
	SplitHTTPSettings *V2RayXHTTPOptions              `json:"splithttpSettings,omitempty"`
}

type V2RayXHTTPTLSCompatOptions struct {
	ServerName    string                     `json:"serverName,omitempty"`
	ALPN          badoption.Listable[string] `json:"alpn,omitempty"`
	AllowInsecure bool                       `json:"allowInsecure,omitempty"`
	Fingerprint   string                     `json:"fingerprint,omitempty"`
}

type V2RayXHTTPRealityCompatOptions struct {
	ServerName  string `json:"serverName,omitempty"`
	PublicKey   string `json:"publicKey,omitempty"`
	ShortID     string `json:"shortId,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

func (o *V2RayXHTTPOptions) UnmarshalJSON(data []byte) error {
	type v2rayXHTTPOptions V2RayXHTTPOptions
	if err := json.Unmarshal(data, (*v2rayXHTTPOptions)(o)); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	readRangeAlias := func(target **V2RayXHTTPRangeOptions, names ...string) error {
		for _, name := range names {
			if value, loaded := raw[name]; loaded {
				var rangeValue V2RayXHTTPRangeOptions
				if err := json.Unmarshal(value, &rangeValue); err != nil {
					return err
				}
				*target = &rangeValue
				return nil
			}
		}
		return nil
	}
	if err := readRangeAlias(&o.XPaddingBytes, "xPaddingBytes", "x-padding-bytes"); err != nil {
		return err
	}
	if err := readRangeAlias(&o.XPaddingBytes, "x_padding_bytes"); err != nil {
		return err
	}
	if err := readRangeAlias(&o.ScMaxEachPostBytes, "scMaxEachPostBytes"); err != nil {
		return err
	}
	if err := readRangeAlias(&o.ScMinPostsIntervalMs, "scMinPostsIntervalMs"); err != nil {
		return err
	}
	if err := readRangeAlias(&o.ScStreamUpServerSecs, "scStreamUpServerSecs"); err != nil {
		return err
	}
	if err := readRangeAlias(&o.UplinkChunkSize, "uplinkChunkSize"); err != nil {
		return err
	}
	if err := readRangeAlias(&o.SessionIDLength, "sessionIDLength"); err != nil {
		return err
	}
	readStringAlias := func(target *string, names ...string) error {
		for _, name := range names {
			if value, loaded := raw[name]; loaded {
				return json.Unmarshal(value, target)
			}
		}
		return nil
	}
	if err := readStringAlias(&o.UplinkHTTPMethod, "uplinkHTTPMethod"); err != nil {
		return err
	}
	if err := readStringAlias(&o.XPaddingKey, "xPaddingKey"); err != nil {
		return err
	}
	if err := readStringAlias(&o.XPaddingHeader, "xPaddingHeader"); err != nil {
		return err
	}
	if err := readStringAlias(&o.XPaddingPlacement, "xPaddingPlacement"); err != nil {
		return err
	}
	if err := readStringAlias(&o.XPaddingMethod, "xPaddingMethod"); err != nil {
		return err
	}
	if err := readStringAlias(&o.SessionIDPlacement, "sessionIDPlacement"); err != nil {
		return err
	}
	if err := readStringAlias(&o.SessionIDKey, "sessionIDKey"); err != nil {
		return err
	}
	if err := readStringAlias(&o.SessionIDTable, "sessionIDTable"); err != nil {
		return err
	}
	if err := readStringAlias(&o.SeqPlacement, "seqPlacement"); err != nil {
		return err
	}
	if err := readStringAlias(&o.SeqKey, "seqKey"); err != nil {
		return err
	}
	if err := readStringAlias(&o.UplinkDataPlacement, "uplinkDataPlacement"); err != nil {
		return err
	}
	if err := readStringAlias(&o.UplinkDataKey, "uplinkDataKey"); err != nil {
		return err
	}
	readBoolAlias := func(target *bool, names ...string) error {
		for _, name := range names {
			if value, loaded := raw[name]; loaded {
				return json.Unmarshal(value, target)
			}
		}
		return nil
	}
	if err := readBoolAlias(&o.XPaddingObfsMode, "xPaddingObfsMode"); err != nil {
		return err
	}
	if err := readBoolAlias(&o.NoGRPCHeader, "noGRPCHeader"); err != nil {
		return err
	}
	if err := readBoolAlias(&o.NoSSEHeader, "noSSEHeader"); err != nil {
		return err
	}
	readIntAlias := func(target *int, names ...string) error {
		for _, name := range names {
			if value, loaded := raw[name]; loaded {
				return json.Unmarshal(value, target)
			}
		}
		return nil
	}
	if err := readIntAlias(&o.ScMaxBufferedPosts, "scMaxBufferedPosts"); err != nil {
		return err
	}
	if err := readIntAlias(&o.ServerMaxHeaderBytes, "serverMaxHeaderBytes"); err != nil {
		return err
	}
	readObjectAlias := func(target any, names ...string) error {
		for _, name := range names {
			if value, loaded := raw[name]; loaded {
				return json.Unmarshal(value, target)
			}
		}
		return nil
	}
	if o.Xmux == nil {
		var xmux V2RayXHTTPXMuxOptions
		if err := readObjectAlias(&xmux, "xmux"); err != nil {
			return err
		}
		if xmux != (V2RayXHTTPXMuxOptions{}) {
			o.Xmux = &xmux
		}
	}
	if o.DownloadSettings == nil {
		var downloadSettings V2RayXHTTPDownloadOptions
		if err := readObjectAlias(&downloadSettings, "downloadSettings", "download-settings"); err != nil {
			return err
		}
		if !downloadSettings.isEmpty() {
			o.DownloadSettings = &downloadSettings
		}
	}
	if extraValue, loaded := raw["extra"]; loaded {
		var extra V2RayXHTTPOptions
		if err := json.Unmarshal(extraValue, &extra); err != nil {
			return err
		}
		if extra.Xmux != nil {
			o.Xmux = extra.Xmux
		}
		if extra.DownloadSettings != nil {
			o.DownloadSettings = extra.DownloadSettings
		}
	}
	return nil
}

func (o *V2RayXHTTPXMuxOptions) UnmarshalJSON(data []byte) error {
	type v2rayXHTTPXMuxOptions V2RayXHTTPXMuxOptions
	if err := json.Unmarshal(data, (*v2rayXHTTPXMuxOptions)(o)); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	readRangeAlias := func(target **V2RayXHTTPRangeOptions, names ...string) error {
		for _, name := range names {
			if value, loaded := raw[name]; loaded {
				var rangeValue V2RayXHTTPRangeOptions
				if err := json.Unmarshal(value, &rangeValue); err != nil {
					return err
				}
				*target = &rangeValue
				return nil
			}
		}
		return nil
	}
	if err := readRangeAlias(&o.MaxConcurrency, "maxConcurrency"); err != nil {
		return err
	}
	if err := readRangeAlias(&o.MaxConnections, "maxConnections"); err != nil {
		return err
	}
	if err := readRangeAlias(&o.CMaxReuseTimes, "cMaxReuseTimes"); err != nil {
		return err
	}
	if err := readRangeAlias(&o.HMaxRequestTimes, "hMaxRequestTimes"); err != nil {
		return err
	}
	if err := readRangeAlias(&o.HMaxReusableSecs, "hMaxReusableSecs"); err != nil {
		return err
	}
	if value, loaded := raw["hKeepAlivePeriod"]; loaded {
		return json.Unmarshal(value, &o.HKeepAlivePeriod)
	}
	return nil
}

func (o V2RayXHTTPDownloadOptions) isEmpty() bool {
	return o.Server == "" &&
		o.ServerPort == 0 &&
		reflect.DeepEqual(o.DialerOptions, DialerOptions{}) &&
		o.TLS == nil &&
		o.Transport == nil &&
		o.Address == "" &&
		o.Port == 0 &&
		o.Network == "" &&
		o.Security == "" &&
		o.TLSSettings == nil &&
		o.RealitySettings == nil &&
		o.XHTTPSettings == nil &&
		o.SplitHTTPSettings == nil
}

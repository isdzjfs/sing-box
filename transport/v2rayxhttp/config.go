package v2rayxhttp

import (
	"encoding/base64"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"

	"github.com/gofrs/uuid/v5"
	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
)

const (
	modeAuto      = "auto"
	modePacketUp  = "packet-up"
	modeStreamUp  = "stream-up"
	modeStreamOne = "stream-one"

	placementQueryInHeader = "queryInHeader"
	placementCookie        = "cookie"
	placementHeader        = "header"
	placementQuery         = "query"
	placementPath          = "path"
	placementBody          = "body"
	placementAuto          = "auto"

	paddingMethodRepeatX  = "repeat-x"
	paddingMethodTokenish = "tokenish"
)

type rangeConfig struct {
	from int32
	to   int32
}

func newRangeConfig(options *option.V2RayXHTTPRangeOptions) *rangeConfig {
	if options == nil {
		return nil
	}
	return &rangeConfig{from: options.From, to: options.To}
}

func (r *rangeConfig) rand() int32 {
	if r == nil || r.to <= 0 {
		return 0
	}
	if r.to <= r.from {
		return r.from
	}
	return r.from + int32(rand.Int64N(int64(r.to-r.from+1)))
}

type config struct {
	host                 string
	path                 string
	mode                 string
	headers              http.Header
	xPaddingBytes        *rangeConfig
	xPaddingObfsMode     bool
	xPaddingKey          string
	xPaddingHeader       string
	xPaddingPlacement    string
	xPaddingMethod       string
	uplinkHTTPMethod     string
	sessionIDPlacement   string
	sessionIDKey         string
	sessionIDTable       string
	sessionIDLength      *rangeConfig
	seqPlacement         string
	seqKey               string
	uplinkDataPlacement  string
	uplinkDataKey        string
	uplinkChunkSize      *rangeConfig
	noGRPCHeader         bool
	noSSEHeader          bool
	scMaxEachPostBytes   *rangeConfig
	scMinPostsIntervalMs *rangeConfig
	scMaxBufferedPosts   int
	scStreamUpServerSecs *rangeConfig
	serverMaxHeaderBytes int
	xmux                 *xmuxConfig
}

func newConfig(options option.V2RayXHTTPOptions) (*config, error) {
	if options.DownloadSettings != nil && options.Mode == modeStreamOne {
		return nil, E.New(`download_settings cannot be used in "stream-one" mode`)
	}
	for key := range options.Headers {
		if strings.EqualFold(key, "host") {
			return nil, E.New(`"headers" can't contain "host"`)
		}
	}
	xmux, err := newXMuxConfig(options.Xmux)
	if err != nil {
		return nil, err
	}
	config := &config{
		host:                 options.Host,
		path:                 options.Path,
		mode:                 options.Mode,
		headers:              options.Headers.Build(),
		xPaddingBytes:        newRangeConfig(options.XPaddingBytes),
		xPaddingObfsMode:     options.XPaddingObfsMode,
		xPaddingKey:          options.XPaddingKey,
		xPaddingHeader:       options.XPaddingHeader,
		xPaddingPlacement:    options.XPaddingPlacement,
		xPaddingMethod:       options.XPaddingMethod,
		uplinkHTTPMethod:     options.UplinkHTTPMethod,
		sessionIDPlacement:   options.SessionIDPlacement,
		sessionIDKey:         options.SessionIDKey,
		sessionIDTable:       options.SessionIDTable,
		sessionIDLength:      newRangeConfig(options.SessionIDLength),
		seqPlacement:         options.SeqPlacement,
		seqKey:               options.SeqKey,
		uplinkDataPlacement:  options.UplinkDataPlacement,
		uplinkDataKey:        options.UplinkDataKey,
		uplinkChunkSize:      newRangeConfig(options.UplinkChunkSize),
		noGRPCHeader:         options.NoGRPCHeader,
		noSSEHeader:          options.NoSSEHeader,
		scMaxEachPostBytes:   newRangeConfig(options.ScMaxEachPostBytes),
		scMinPostsIntervalMs: newRangeConfig(options.ScMinPostsIntervalMs),
		scMaxBufferedPosts:   options.ScMaxBufferedPosts,
		scStreamUpServerSecs: newRangeConfig(options.ScStreamUpServerSecs),
		serverMaxHeaderBytes: options.ServerMaxHeaderBytes,
		xmux:                 xmux,
	}
	if config.mode == "" {
		config.mode = modeAuto
	}
	switch config.mode {
	case modeAuto, modePacketUp, modeStreamUp, modeStreamOne:
	default:
		return nil, E.New("unsupported xhttp mode: ", config.mode)
	}
	if config.xPaddingBytes != nil && (config.xPaddingBytes.from <= 0 || config.xPaddingBytes.to <= 0) {
		return nil, E.New("x_padding_bytes cannot be disabled")
	}
	if config.xPaddingKey == "" {
		config.xPaddingKey = "x_padding"
	}
	if config.xPaddingHeader == "" {
		config.xPaddingHeader = "X-Padding"
	}
	switch config.xPaddingPlacement {
	case "":
		config.xPaddingPlacement = placementQueryInHeader
	case placementCookie, placementHeader, placementQuery, placementQueryInHeader:
	default:
		return nil, E.New("unsupported xhttp padding placement: ", config.xPaddingPlacement)
	}
	switch config.xPaddingMethod {
	case "":
		config.xPaddingMethod = paddingMethodRepeatX
	case paddingMethodRepeatX, paddingMethodTokenish:
	default:
		return nil, E.New("unsupported xhttp padding method: ", config.xPaddingMethod)
	}
	switch config.uplinkDataPlacement {
	case "":
		config.uplinkDataPlacement = placementAuto
	case placementAuto, placementBody:
	case placementCookie, placementHeader:
		if config.mode != modePacketUp {
			return nil, E.New("uplink_data_placement can be ", config.uplinkDataPlacement, " only in packet-up mode")
		}
	default:
		return nil, E.New("unsupported xhttp uplink data placement: ", config.uplinkDataPlacement)
	}
	if config.uplinkHTTPMethod == "" {
		config.uplinkHTTPMethod = http.MethodPost
	}
	config.uplinkHTTPMethod = strings.ToUpper(config.uplinkHTTPMethod)
	if config.uplinkHTTPMethod == http.MethodGet && config.mode != modePacketUp {
		return nil, E.New("uplink_http_method can be GET only in packet-up mode")
	}
	switch config.sessionIDPlacement {
	case "":
		config.sessionIDPlacement = placementPath
	case placementPath, placementCookie, placementHeader, placementQuery:
	default:
		return nil, E.New("unsupported xhttp session placement: ", config.sessionIDPlacement)
	}
	switch config.seqPlacement {
	case "":
		config.seqPlacement = placementPath
	case placementPath, placementCookie, placementHeader, placementQuery:
	default:
		return nil, E.New("unsupported xhttp seq placement: ", config.seqPlacement)
	}
	if config.sessionIDPlacement != placementPath && config.sessionIDKey == "" {
		switch config.sessionIDPlacement {
		case placementCookie, placementQuery:
			config.sessionIDKey = "x_session"
		case placementHeader:
			config.sessionIDKey = "X-Session"
		}
	}
	if config.seqPlacement != placementPath && config.seqKey == "" {
		switch config.seqPlacement {
		case placementCookie, placementQuery:
			config.seqKey = "x_seq"
		case placementHeader:
			config.seqKey = "X-Seq"
		}
	}
	if config.uplinkDataPlacement != placementBody && config.uplinkDataKey == "" {
		switch config.uplinkDataPlacement {
		case placementCookie:
			config.uplinkDataKey = "x_data"
		case placementAuto, placementHeader:
			config.uplinkDataKey = "X-Data"
		}
	}
	if config.serverMaxHeaderBytes < 0 {
		return nil, E.New("invalid negative value of server_max_header_bytes")
	}
	return config, nil
}

func (c *config) normalizedPath() string {
	pathAndQuery := strings.SplitN(c.path, "?", 2)
	path := pathAndQuery[0]
	if path == "" || path[0] != '/' {
		path = "/" + path
	}
	if path[len(path)-1] != '/' {
		path += "/"
	}
	return path
}

func (c *config) normalizedQuery() string {
	pathAndQuery := strings.SplitN(c.path, "?", 2)
	if len(pathAndQuery) > 1 {
		return pathAndQuery[1]
	}
	return ""
}

func (c *config) requestHeader() http.Header {
	header := c.headers.Clone()
	applyDefaultFetchHeaders(header)
	return header
}

func (c *config) normalizedXPaddingBytes() *rangeConfig {
	if c.xPaddingBytes == nil || c.xPaddingBytes.to == 0 {
		return &rangeConfig{from: 100, to: 1000}
	}
	return c.xPaddingBytes
}

func (c *config) normalizedScMaxEachPostBytes() *rangeConfig {
	if c.scMaxEachPostBytes == nil || c.scMaxEachPostBytes.to == 0 {
		return &rangeConfig{from: 1000000, to: 1000000}
	}
	return c.scMaxEachPostBytes
}

func (c *config) normalizedScMinPostsIntervalMs() *rangeConfig {
	if c.scMinPostsIntervalMs == nil || c.scMinPostsIntervalMs.to == 0 {
		return &rangeConfig{from: 30, to: 30}
	}
	return c.scMinPostsIntervalMs
}

func (c *config) normalizedScMaxBufferedPosts() int {
	if c.scMaxBufferedPosts == 0 {
		return 30
	}
	return c.scMaxBufferedPosts
}

func (c *config) normalizedScStreamUpServerSecs() *rangeConfig {
	if c.scStreamUpServerSecs == nil || c.scStreamUpServerSecs.to == 0 {
		return &rangeConfig{from: 20, to: 80}
	}
	return c.scStreamUpServerSecs
}

func (c *config) normalizedUplinkChunkSize() *rangeConfig {
	if c.uplinkChunkSize == nil || c.uplinkChunkSize.to == 0 {
		switch c.uplinkDataPlacement {
		case placementCookie:
			return &rangeConfig{from: 2 * 1024, to: 3 * 1024}
		case placementHeader:
			return &rangeConfig{from: 3 * 1000, to: 4 * 1000}
		default:
			return c.normalizedScMaxEachPostBytes()
		}
	} else if c.uplinkChunkSize.from < 64 {
		return &rangeConfig{from: 64, to: max(64, c.uplinkChunkSize.to)}
	}
	return c.uplinkChunkSize
}

func (c *config) normalizedServerMaxHeaderBytes() int {
	if c.serverMaxHeaderBytes <= 0 {
		return 8192
	}
	return c.serverMaxHeaderBytes
}

func (c *config) generateSessionID() string {
	length := c.sessionIDLength.rand()
	table := c.sessionIDTable
	if predefined, ok := predefinedSessionIDTables[table]; ok {
		table = predefined
	}
	if table != "" && length > 0 {
		id := make([]byte, length)
		for i := range id {
			id[i] = table[rand.IntN(len(table))]
		}
		return string(id)
	}
	id, _ := uuid.NewV4()
	return id.String()
}

func (c *config) applyMetaToRequest(request *http.Request, sessionID string, seq string) {
	apply := func(placement string, key string, value string) {
		if value == "" {
			return
		}
		switch placement {
		case placementPath:
			request.URL.Path = appendToPath(request.URL.Path, value)
		case placementQuery:
			q := request.URL.Query()
			q.Set(key, value)
			request.URL.RawQuery = q.Encode()
		case placementHeader:
			request.Header.Set(key, value)
		case placementCookie:
			request.AddCookie(&http.Cookie{Name: key, Value: value})
		}
	}
	apply(c.sessionIDPlacement, c.sessionIDKey, sessionID)
	apply(c.seqPlacement, c.seqKey, seq)
}

func (c *config) extractMetaFromRequest(request *http.Request, path string) (string, string) {
	var subpath []string
	pathPart := 0
	if c.sessionIDPlacement == placementPath || c.seqPlacement == placementPath {
		subpath = strings.Split(request.URL.Path[len(path):], "/")
	}
	read := func(placement string, key string) string {
		switch placement {
		case placementPath:
			if len(subpath) > pathPart {
				value := subpath[pathPart]
				pathPart++
				return value
			}
		case placementQuery:
			return request.URL.Query().Get(key)
		case placementHeader:
			return request.Header.Get(key)
		case placementCookie:
			if cookie, err := request.Cookie(key); err == nil {
				return cookie.Value
			}
		}
		return ""
	}
	return read(c.sessionIDPlacement, c.sessionIDKey), read(c.seqPlacement, c.seqKey)
}

func (c *config) fillStreamRequest(request *http.Request, sessionID string) {
	request.Header = c.requestHeader()
	c.applyRequestPadding(request)
	c.applyMetaToRequest(request, sessionID, "")
	if request.Body != nil && !c.noGRPCHeader {
		request.Header.Set("Content-Type", "application/grpc")
	}
}

func (c *config) fillPacketRequest(request *http.Request, sessionID string, seq string, payload []byte) {
	dataPlacement := c.uplinkDataPlacement
	if dataPlacement == placementBody || dataPlacement == placementAuto {
		request.Header = c.requestHeader()
		request.Body = ioNopReadCloser(payload)
		request.ContentLength = int64(len(payload))
	} else {
		switch dataPlacement {
		case placementHeader:
			request.Header = c.requestHeaderWithPayload(payload)
		case placementCookie:
			request.Header = c.requestHeader()
			for _, cookie := range c.requestCookiesWithPayload(payload) {
				request.AddCookie(cookie)
			}
		}
	}
	c.applyRequestPadding(request)
	c.applyMetaToRequest(request, sessionID, seq)
}

func (c *config) requestHeaderWithPayload(payload []byte) http.Header {
	header := c.requestHeader()
	encodedData := base64.RawURLEncoding.EncodeToString(payload)
	for i := 0; len(encodedData) > 0; i++ {
		chunkSize := min(int(c.normalizedUplinkChunkSize().rand()), len(encodedData))
		chunk := encodedData[:chunkSize]
		encodedData = encodedData[chunkSize:]
		header.Set(fmt.Sprintf("%s-%d", c.uplinkDataKey, i), chunk)
	}
	return header
}

func (c *config) requestCookiesWithPayload(payload []byte) []*http.Cookie {
	var cookies []*http.Cookie
	encodedData := base64.RawURLEncoding.EncodeToString(payload)
	for i := 0; len(encodedData) > 0; i++ {
		chunkSize := min(int(c.normalizedUplinkChunkSize().rand()), len(encodedData))
		chunk := encodedData[:chunkSize]
		encodedData = encodedData[chunkSize:]
		cookies = append(cookies, &http.Cookie{Name: fmt.Sprintf("%s_%d", c.uplinkDataKey, i), Value: chunk})
	}
	return cookies
}

func appendToPath(path string, value string) string {
	if strings.HasSuffix(path, "/") {
		return path + value
	}
	return path + "/" + value
}

func matchHost(requestHost string, expectedHost string) bool {
	if expectedHost == "" {
		return true
	}
	if strings.EqualFold(requestHost, expectedHost) {
		return true
	}
	host := requestHost
	if strings.Contains(requestHost, ":") {
		if parsedHost, _, err := netSplitHostPort(requestHost); err == nil {
			host = parsedHost
		}
	}
	return strings.EqualFold(host, expectedHost)
}

func applyPaddingToQuery(u *url.URL, key string, value string) {
	if u == nil || key == "" || value == "" {
		return
	}
	q := u.Query()
	q.Set(key, value)
	u.RawQuery = q.Encode()
}

var predefinedSessionIDTables = map[string]string{
	"ALPHABET": "ABCDEFGHIJKLMNOPQRSTUVWXYZ",
	"Alphabet": "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz",
	"BASE36":   "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ",
	"Base62":   "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz",
	"HEX":      "0123456789ABCDEF",
	"alphabet": "abcdefghijklmnopqrstuvwxyz",
	"base36":   "0123456789abcdefghijklmnopqrstuvwxyz",
	"hex":      "0123456789abcdef",
	"number":   "0123456789",
}

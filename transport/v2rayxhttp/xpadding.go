package v2rayxhttp

import (
	"crypto/rand"
	"math"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/http2/hpack"
)

const (
	charsetBase62                = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	avgHuffmanBytesPerCharBase62 = 0.8
	validationTolerance          = 2
)

func generatePadding(method string, length int) string {
	if length <= 0 {
		return ""
	}
	switch method {
	case paddingMethodTokenish:
		paddingValue := generateTokenishPaddingBase62(length)
		if paddingValue != "" {
			return paddingValue
		}
	}
	return strings.Repeat("X", length)
}

func generateTokenishPaddingBase62(targetHuffmanBytes int) string {
	n := int(math.Ceil(float64(targetHuffmanBytes) / avgHuffmanBytesPerCharBase62))
	if n < 1 {
		n = 1
	}
	value, ok := randStringFromCharset(n, charsetBase62)
	if !ok {
		return ""
	}
	const maxIter = 150
	adjustChar := byte('X')
	for iter := 0; iter < maxIter; iter++ {
		currentLength := int(hpack.HuffmanEncodeLength(value))
		diff := currentLength - targetHuffmanBytes
		if absInt(diff) <= validationTolerance {
			return value
		}
		if diff < 0 {
			value += string(adjustChar)
			if adjustChar == 'X' {
				adjustChar = 'Z'
			} else {
				adjustChar = 'X'
			}
		} else {
			if len(value) <= 1 {
				return value
			}
			value = value[:len(value)-1]
		}
	}
	return value
}

func randStringFromCharset(n int, charset string) (string, bool) {
	if n <= 0 || len(charset) == 0 {
		return "", false
	}
	m := len(charset)
	limit := byte(256 - (256 % m))
	result := make([]byte, n)
	buf := make([]byte, 256)
	i := 0
	for i < n {
		if _, err := rand.Read(buf); err != nil {
			return "", false
		}
		for _, rb := range buf {
			if rb >= limit {
				continue
			}
			result[i] = charset[int(rb)%m]
			i++
			if i == n {
				break
			}
		}
	}
	return string(result), true
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func (c *config) applyRequestPadding(request *http.Request) {
	length := int(c.normalizedXPaddingBytes().rand())
	if c.xPaddingObfsMode {
		c.applyXPaddingToRequest(request, c.xPaddingPlacement, c.xPaddingKey, c.xPaddingHeader, request.URL.String(), c.xPaddingMethod, length)
		return
	}
	c.applyXPaddingToRequest(request, placementQueryInHeader, "x_padding", "Referer", request.URL.String(), paddingMethodRepeatX, length)
}

func (c *config) applyResponsePadding(writer http.ResponseWriter) {
	length := int(c.normalizedXPaddingBytes().rand())
	if c.xPaddingObfsMode {
		c.applyXPaddingToHeader(writer.Header(), c.xPaddingPlacement, c.xPaddingKey, c.xPaddingHeader, "", c.xPaddingMethod, length)
		return
	}
	c.applyXPaddingToHeader(writer.Header(), placementHeader, "", "X-Padding", "", paddingMethodRepeatX, length)
}

func (c *config) applyXPaddingToRequest(request *http.Request, placement string, key string, header string, rawURL string, method string, length int) {
	if request.Header == nil {
		request.Header = make(http.Header)
	}
	if placement == placementHeader || placement == placementQueryInHeader {
		c.applyXPaddingToHeader(request.Header, placement, key, header, rawURL, method, length)
		return
	}
	paddingValue := generatePadding(method, length)
	switch placement {
	case placementCookie:
		request.AddCookie(&http.Cookie{Name: key, Value: paddingValue, Path: "/"})
	case placementQuery:
		applyPaddingToQuery(request.URL, key, paddingValue)
	}
}

func (c *config) applyXPaddingToHeader(headerMap http.Header, placement string, key string, header string, rawURL string, method string, length int) {
	if headerMap == nil {
		return
	}
	paddingValue := generatePadding(method, length)
	switch placement {
	case placementHeader:
		headerMap.Set(header, paddingValue)
	case placementQueryInHeader:
		u, err := url.Parse(rawURL)
		if err != nil || u == nil {
			return
		}
		u.RawQuery = key + "=" + paddingValue
		headerMap.Set(header, u.String())
	}
}

func (c *config) extractXPaddingFromRequest(request *http.Request) string {
	if request == nil {
		return ""
	}
	if !c.xPaddingObfsMode {
		referrer := request.Header.Get("Referer")
		if referrer != "" {
			if referrerURL, err := url.Parse(referrer); err == nil {
				return referrerURL.Query().Get("x_padding")
			}
		}
		return request.URL.Query().Get("x_padding")
	}
	if cookie, err := request.Cookie(c.xPaddingKey); err == nil && cookie != nil && cookie.Value != "" {
		return cookie.Value
	}
	headerValue := request.Header.Get(c.xPaddingHeader)
	if headerValue != "" {
		if c.xPaddingPlacement == placementHeader {
			return headerValue
		}
		if parsedURL, err := url.Parse(headerValue); err == nil {
			return parsedURL.Query().Get(c.xPaddingKey)
		}
	}
	return request.URL.Query().Get(c.xPaddingKey)
}

func (c *config) isPaddingValid(paddingValue string) bool {
	if paddingValue == "" {
		return false
	}
	validRange := c.normalizedXPaddingBytes()
	switch c.xPaddingMethod {
	case paddingMethodTokenish:
		n := int32(hpack.HuffmanEncodeLength(paddingValue))
		from := validRange.from - validationTolerance
		to := validRange.to + validationTolerance
		if from < 0 {
			from = 0
		}
		return n >= from && n <= to
	default:
		n := int32(len(paddingValue))
		return n >= validRange.from && n <= validRange.to
	}
}

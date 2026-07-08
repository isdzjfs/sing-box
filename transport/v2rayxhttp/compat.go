package v2rayxhttp

import (
	"bytes"
	"io"
	"net"
)

func ioNopReadCloser(payload []byte) io.ReadCloser {
	return io.NopCloser(bytes.NewReader(payload))
}

func netSplitHostPort(address string) (host string, port string, err error) {
	return net.SplitHostPort(address)
}

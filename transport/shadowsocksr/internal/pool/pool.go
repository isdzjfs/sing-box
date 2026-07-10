package pool

import (
	"bytes"
	"sync"

	"github.com/sagernet/sing/common/buf"
)

const (
	RelayBufferSize = buf.BufferSize
	UDPBufferSize   = buf.UDPBufferSize
)

var bufferPool = sync.Pool{
	New: func() any {
		return &bytes.Buffer{}
	},
}

func Get(size int) []byte {
	buffer := buf.Get(size)
	if buffer != nil {
		return buffer
	}
	return make([]byte, size)
}

func Put(buffer []byte) error {
	if cap(buffer) > buf.MaxPooledBufferSize {
		return nil
	}
	return buf.Put(buffer)
}

func GetBuffer() *bytes.Buffer {
	return bufferPool.Get().(*bytes.Buffer)
}

func PutBuffer(buffer *bytes.Buffer) {
	buffer.Reset()
	bufferPool.Put(buffer)
}

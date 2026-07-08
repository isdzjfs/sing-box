package v2rayxhttp

import (
	"container/heap"
	"io"
	"sync"
	"sync/atomic"

	E "github.com/sagernet/sing/common/exceptions"
)

type packet struct {
	reader  *serverHTTPConn
	payload []byte
	seq     uint64
}

type uploadQueue struct {
	reader        atomic.Pointer[serverHTTPConn]
	pushedPackets chan packet
	heap          uploadHeap
	nextSeq       uint64
	maxPackets    int
	closed        chan struct{}
	closeOnce     sync.Once
}

func newUploadQueue(maxPackets int) *uploadQueue {
	return &uploadQueue{
		pushedPackets: make(chan packet, maxPackets),
		heap:          uploadHeap{},
		maxPackets:    maxPackets,
		closed:        make(chan struct{}),
	}
}

func (q *uploadQueue) Push(packet packet) error {
	if q.reader.Load() != nil || (packet.reader != nil && !q.reader.CompareAndSwap(nil, packet.reader)) {
		return E.New("xhttp upload reader already exists")
	}
	select {
	case q.pushedPackets <- packet:
		select {
		case <-q.closed:
			return E.New("xhttp packet queue closed")
		default:
		}
		return nil
	case <-q.closed:
		return E.New("xhttp packet queue closed")
	}
}

func (q *uploadQueue) Close() error {
	q.closeOnce.Do(func() {
		close(q.closed)
	})
	if reader := q.reader.Load(); reader != nil {
		return reader.Close()
	}
	return nil
}

func (q *uploadQueue) Read(b []byte) (int, error) {
	if reader := q.reader.Load(); reader != nil {
		return reader.Read(b)
	}
	select {
	case <-q.closed:
		return 0, io.EOF
	default:
	}
	if len(q.heap) == 0 {
		select {
		case packet := <-q.pushedPackets:
			if packet.reader != nil {
				return packet.reader.Read(b)
			}
			heap.Push(&q.heap, packet)
		case <-q.closed:
			return 0, io.EOF
		}
	}
	for len(q.heap) > 0 {
		packet := heap.Pop(&q.heap).(packet)
		if packet.seq == q.nextSeq {
			n := copy(b, packet.payload)
			if n < len(packet.payload) {
				packet.payload = packet.payload[n:]
				heap.Push(&q.heap, packet)
			} else {
				q.nextSeq = packet.seq + 1
			}
			return n, nil
		}
		if packet.seq > q.nextSeq {
			if len(q.heap) > q.maxPackets {
				return 0, E.New("xhttp packet queue is too large")
			}
			heap.Push(&q.heap, packet)
			select {
			case nextPacket := <-q.pushedPackets:
				heap.Push(&q.heap, nextPacket)
			case <-q.closed:
				return 0, io.EOF
			}
		}
	}
	return 0, nil
}

type uploadHeap []packet

func (h uploadHeap) Len() int           { return len(h) }
func (h uploadHeap) Less(i, j int) bool { return h[i].seq < h[j].seq }
func (h uploadHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *uploadHeap) Push(x any) {
	*h = append(*h, x.(packet))
}

func (h *uploadHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

package v2rayxhttp

import (
	"io"
	"net"
	"os"
	"sync"
	"time"
)

type splitConn struct {
	writer     io.WriteCloser
	reader     io.ReadCloser
	remoteAddr net.Addr
	localAddr  net.Addr
	onClose    func()
	closeOnce  sync.Once
}

func (c *splitConn) Write(b []byte) (int, error) {
	return c.writer.Write(b)
}

func (c *splitConn) Read(b []byte) (int, error) {
	return c.reader.Read(b)
}

func (c *splitConn) Close() error {
	c.closeOnce.Do(func() {
		if c.onClose != nil {
			c.onClose()
		}
	})
	err := c.writer.Close()
	err2 := c.reader.Close()
	if err != nil {
		return err
	}
	return err2
}

func (c *splitConn) LocalAddr() net.Addr {
	if c.localAddr == nil {
		return emptyAddr{}
	}
	return c.localAddr
}

func (c *splitConn) RemoteAddr() net.Addr {
	if c.remoteAddr == nil {
		return emptyAddr{}
	}
	return c.remoteAddr
}

func (c *splitConn) SetDeadline(time.Time) error {
	return os.ErrInvalid
}

func (c *splitConn) SetReadDeadline(time.Time) error {
	return os.ErrInvalid
}

func (c *splitConn) SetWriteDeadline(time.Time) error {
	return os.ErrInvalid
}

type emptyAddr struct{}

func (emptyAddr) Network() string { return "xhttp" }
func (emptyAddr) String() string  { return "xhttp" }

type waitReadCloser struct {
	access sync.Mutex
	wait   chan struct{}
	once   sync.Once
	reader io.ReadCloser
	err    error
	closed bool
}

func newWaitReadCloser() *waitReadCloser {
	return &waitReadCloser{wait: make(chan struct{})}
}

func (w *waitReadCloser) Set(reader io.ReadCloser) {
	w.access.Lock()
	if w.closed {
		w.access.Unlock()
		_ = reader.Close()
		return
	}
	w.reader = reader
	w.access.Unlock()
	w.signal()
}

func (w *waitReadCloser) SetError(err error) {
	if err == nil {
		err = io.ErrClosedPipe
	}
	w.access.Lock()
	if w.reader == nil && w.err == nil {
		w.err = err
	}
	w.access.Unlock()
	w.signal()
}

func (w *waitReadCloser) signal() {
	w.once.Do(func() {
		close(w.wait)
	})
}

func (w *waitReadCloser) Read(b []byte) (int, error) {
	<-w.wait
	w.access.Lock()
	reader := w.reader
	err := w.err
	w.access.Unlock()
	if reader == nil {
		if err == nil {
			err = io.ErrClosedPipe
		}
		return 0, err
	}
	return reader.Read(b)
}

func (w *waitReadCloser) Close() error {
	w.access.Lock()
	w.closed = true
	reader := w.reader
	if reader == nil && w.err == nil {
		w.err = io.ErrClosedPipe
	}
	w.access.Unlock()
	w.signal()
	if reader != nil {
		return reader.Close()
	}
	return nil
}

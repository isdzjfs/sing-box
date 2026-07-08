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
	wait chan struct{}
	once sync.Once
	io.ReadCloser
}

func newWaitReadCloser() *waitReadCloser {
	return &waitReadCloser{wait: make(chan struct{})}
}

func (w *waitReadCloser) Set(reader io.ReadCloser) {
	w.ReadCloser = reader
	w.once.Do(func() {
		close(w.wait)
	})
}

func (w *waitReadCloser) Read(b []byte) (int, error) {
	if w.ReadCloser == nil {
		<-w.wait
		if w.ReadCloser == nil {
			return 0, io.ErrClosedPipe
		}
	}
	return w.ReadCloser.Read(b)
}

func (w *waitReadCloser) Close() error {
	if w.ReadCloser != nil {
		return w.ReadCloser.Close()
	}
	w.once.Do(func() {
		close(w.wait)
	})
	if w.ReadCloser != nil {
		return w.ReadCloser.Close()
	}
	return nil
}

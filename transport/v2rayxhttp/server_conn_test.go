package v2rayxhttp

import (
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type blockingFlushWriter struct {
	http.ResponseWriter
	flushing chan struct{}
	release  chan struct{}
}

func (w *blockingFlushWriter) Write(payload []byte) (int, error) {
	return len(payload), nil
}

func (w *blockingFlushWriter) Flush() {
	close(w.flushing)
	<-w.release
}

func TestServerHTTPConnCloseWaitsForResponseFlush(t *testing.T) {
	writer := &blockingFlushWriter{flushing: make(chan struct{}), release: make(chan struct{})}
	conn := &serverHTTPConn{done: make(chan struct{}), responseWriter: writer}
	writeDone := make(chan error, 1)
	go func() {
		_, err := conn.Write([]byte("response"))
		writeDone <- err
	}()
	<-writer.flushing
	closeStarted := make(chan struct{})
	closeDone := make(chan error, 1)
	go func() {
		close(closeStarted)
		closeDone <- conn.Close()
	}()
	<-closeStarted
	select {
	case <-conn.done:
		close(writer.release)
		<-writeDone
		<-closeDone
		t.Fatal("HTTP handler was allowed to return while its response was flushing")
	case <-time.After(20 * time.Millisecond):
	}
	close(writer.release)
	require.NoError(t, <-writeDone)
	require.NoError(t, <-closeDone)
	_, err := conn.Write([]byte("after close"))
	require.ErrorIs(t, err, io.ErrClosedPipe)
}

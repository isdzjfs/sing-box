package v2rayxhttp

import (
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWaitReadCloserMapsLocalClose(t *testing.T) {
	responseBodyErr := errors.New("http2: response body closed")
	responseBody := newCloseErrorReadCloser(responseBodyErr)
	waitReader := newWaitReadCloser()
	waitReader.Set(responseBody)

	readResult := make(chan error, 1)
	go func() {
		var buffer [1]byte
		_, err := waitReader.Read(buffer[:])
		readResult <- err
	}()
	waitForSignal(t, responseBody.started, "response body read did not start")

	if err := waitReader.Close(); err != nil {
		t.Fatal(err)
	}
	err := waitForError(t, readResult, "response body read did not stop")
	if !errors.Is(err, net.ErrClosed) {
		t.Fatalf("expected local close error, got %v", err)
	}
	if errors.Is(err, responseBodyErr) {
		t.Fatalf("local close leaked HTTP/2 response body error: %v", err)
	}
}

func TestMonitorStreamUploadPropagatesFailure(t *testing.T) {
	uploadErr := errors.New("upload failed")
	uploadReader := newWaitReadCloser()
	downloadBody := newCloseErrorReadCloser(errors.New("http2: response body closed"))
	downloadReader := newWaitReadCloser()
	downloadReader.Set(downloadBody)
	released := make(chan struct{}, 1)
	monitorDone := make(chan struct{})
	go func() {
		monitorStreamUpload(uploadReader, downloadReader, func() { released <- struct{}{} })
		close(monitorDone)
	}()

	readResult := make(chan error, 1)
	go func() {
		var buffer [1]byte
		_, err := downloadReader.Read(buffer[:])
		readResult <- err
	}()
	waitForSignal(t, downloadBody.started, "download read did not start")
	uploadReader.SetError(uploadErr)

	err := waitForError(t, readResult, "upload failure did not stop download read")
	if !errors.Is(err, uploadErr) {
		t.Fatalf("expected upload error, got %v", err)
	}
	if !strings.Contains(err.Error(), "upload XHTTP stream") {
		t.Fatalf("missing XHTTP upload stage: %v", err)
	}
	waitForSignal(t, released, "upload failure did not release connection")
	waitForSignal(t, monitorDone, "upload monitor did not stop")
}

func TestMonitorStreamUploadIgnoresLocalClose(t *testing.T) {
	uploadReader := newWaitReadCloser()
	downloadReader := newWaitReadCloser()
	released := make(chan struct{}, 1)
	monitorDone := make(chan struct{})
	go func() {
		monitorStreamUpload(uploadReader, downloadReader, func() { released <- struct{}{} })
		close(monitorDone)
	}()

	if err := uploadReader.Close(); err != nil {
		t.Fatal(err)
	}
	waitForSignal(t, monitorDone, "upload monitor did not stop after local close")
	select {
	case <-released:
		t.Fatal("local close was treated as an upload failure")
	default:
	}
	_ = downloadReader.Close()
}

type closeErrorReadCloser struct {
	err       error
	started   chan struct{}
	closed    chan struct{}
	startOnce sync.Once
	closeOnce sync.Once
}

func newCloseErrorReadCloser(err error) *closeErrorReadCloser {
	return &closeErrorReadCloser{
		err:     err,
		started: make(chan struct{}),
		closed:  make(chan struct{}),
	}
}

func (r *closeErrorReadCloser) Read([]byte) (int, error) {
	r.startOnce.Do(func() { close(r.started) })
	<-r.closed
	return 0, r.err
}

func (r *closeErrorReadCloser) Close() error {
	r.closeOnce.Do(func() { close(r.closed) })
	return nil
}

func waitForSignal(t *testing.T, signal <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal(message)
	}
}

func waitForError(t *testing.T, result <-chan error, message string) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(time.Second):
		t.Fatal(message)
		return nil
	}
}

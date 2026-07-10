package v2rayxhttp

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

func TestXHTTPOpenStreamReturnsConnectionError(t *testing.T) {
	expectedErr := errors.New("dial failed")
	client := &xhttpClient{
		config: &config{},
		transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return nil, expectedErr
		}),
	}

	_, _, _, err := client.OpenStream(context.Background(), "http://example.com/", "", nil, false)
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected connection error %v, got %v", expectedErr, err)
	}
}

func TestXHTTPPacketUploadFailureUnblocksWriter(t *testing.T) {
	expectedErr := errors.New("upload failed")
	client := &Client{config: &config{}}
	reader, writer := io.Pipe()
	errorHandled := make(chan error, 1)
	loopDone := make(chan struct{})
	go func() {
		defer close(loopDone)
		client.postPacketLoop(
			context.Background(),
			reader,
			"session",
			&clientEndpoint{},
			failingDialerClient{err: expectedErr},
			nil,
			func(err error) { errorHandled <- err },
		)
	}()

	firstWrite := make(chan error, 1)
	go func() {
		_, err := writer.Write([]byte("ping"))
		firstWrite <- err
	}()
	select {
	case err := <-firstWrite:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("initial packet upload write blocked")
	}
	select {
	case err := <-errorHandled:
		if !errors.Is(err, expectedErr) {
			t.Fatalf("expected upload error %v, got %v", expectedErr, err)
		}
	case <-time.After(time.Second):
		t.Fatal("upload failure was not handled")
	}
	select {
	case <-loopDone:
	case <-time.After(time.Second):
		t.Fatal("packet upload loop did not stop")
	}

	_, err := writer.Write([]byte("again"))
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected subsequent write to fail with %v, got %v", expectedErr, err)
	}
}

func TestXHTTPStreamOne(t *testing.T) {
	server, err := NewServer(context.Background(), testLogger{}, option.V2RayXHTTPOptions{
		Path: "/x",
		Mode: modeStreamOne,
		XPaddingBytes: &option.V2RayXHTTPRangeOptions{
			From: 100,
			To:   100,
		},
	}, nil, testHandler{})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go server.Serve(listener)
	defer server.Close()
	client, err := NewClient(context.Background(), N.SystemDialer, M.ParseSocksaddr(listener.Addr().String()), option.V2RayXHTTPOptions{
		Path: "/x",
		Mode: modeStreamOne,
		XPaddingBytes: &option.V2RayXHTTPRangeOptions{
			From: 100,
			To:   100,
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := client.DialContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err = conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 4)
	if _, err = io.ReadFull(conn, response); err != nil {
		t.Fatal(err)
	}
	if string(response) != "pong" {
		t.Fatal("unexpected response: ", string(response))
	}
}

func TestXHTTPPacketUp(t *testing.T) {
	server, err := NewServer(context.Background(), testLogger{}, option.V2RayXHTTPOptions{
		Path: "/x",
		Mode: modePacketUp,
		XPaddingBytes: &option.V2RayXHTTPRangeOptions{
			From: 100,
			To:   100,
		},
		ScMinPostsIntervalMs: &option.V2RayXHTTPRangeOptions{},
	}, nil, testHandler{})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go server.Serve(listener)
	defer server.Close()
	client, err := NewClient(context.Background(), N.SystemDialer, M.ParseSocksaddr(listener.Addr().String()), option.V2RayXHTTPOptions{
		Path: "/x",
		Mode: modePacketUp,
		XPaddingBytes: &option.V2RayXHTTPRangeOptions{
			From: 100,
			To:   100,
		},
		ScMinPostsIntervalMs: &option.V2RayXHTTPRangeOptions{},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := client.DialContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err = conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 4)
	if _, err = io.ReadFull(conn, response); err != nil {
		t.Fatal(err)
	}
	if string(response) != "pong" {
		t.Fatal("unexpected response: ", string(response))
	}
}

func TestXHTTPDownloadSettings(t *testing.T) {
	server, err := NewServer(context.Background(), testLogger{}, option.V2RayXHTTPOptions{
		Path: "/x",
		Mode: modePacketUp,
		XPaddingBytes: &option.V2RayXHTTPRangeOptions{
			From: 100,
			To:   100,
		},
		ScMinPostsIntervalMs: &option.V2RayXHTTPRangeOptions{},
	}, nil, testHandler{})
	if err != nil {
		t.Fatal(err)
	}
	uploadListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer uploadListener.Close()
	downloadListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer downloadListener.Close()
	go server.Serve(uploadListener)
	go server.Serve(downloadListener)
	defer server.Close()
	downloadAddr := M.ParseSocksaddr(downloadListener.Addr().String())
	client, err := NewClient(context.Background(), N.SystemDialer, M.ParseSocksaddr(uploadListener.Addr().String()), option.V2RayXHTTPOptions{
		Path: "/x",
		Mode: modePacketUp,
		XPaddingBytes: &option.V2RayXHTTPRangeOptions{
			From: 100,
			To:   100,
		},
		ScMinPostsIntervalMs: &option.V2RayXHTTPRangeOptions{},
		DownloadSettings: &option.V2RayXHTTPDownloadOptions{
			ServerOptions: option.ServerOptions{
				Server:     downloadAddr.AddrString(),
				ServerPort: downloadAddr.Port,
			},
			Transport: &option.V2RayTransportOptions{
				Type: "xhttp",
				XHTTPOptions: option.V2RayXHTTPOptions{
					Path: "/x",
					XPaddingBytes: &option.V2RayXHTTPRangeOptions{
						From: 100,
						To:   100,
					},
				},
			},
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	conn, err := client.DialContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err = conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 4)
	if _, err = io.ReadFull(conn, response); err != nil {
		t.Fatal(err)
	}
	if string(response) != "pong" {
		t.Fatal("unexpected response: ", string(response))
	}
}

type testHandler struct{}

func (testHandler) NewConnectionEx(ctx context.Context, conn net.Conn, source M.Socksaddr, destination M.Socksaddr, onClose N.CloseHandlerFunc) {
	go func() {
		defer conn.Close()
		if onClose != nil {
			defer onClose(nil)
		}
		buffer := make([]byte, 4)
		if _, err := io.ReadFull(conn, buffer); err != nil {
			return
		}
		_, _ = conn.Write([]byte("pong"))
	}()
}

type testLogger struct{}

func (testLogger) Trace(args ...any)                             {}
func (testLogger) Debug(args ...any)                             {}
func (testLogger) Info(args ...any)                              {}
func (testLogger) Warn(args ...any)                              {}
func (testLogger) Error(args ...any)                             {}
func (testLogger) Fatal(args ...any)                             {}
func (testLogger) Panic(args ...any)                             {}
func (testLogger) TraceContext(ctx context.Context, args ...any) {}
func (testLogger) DebugContext(ctx context.Context, args ...any) {}
func (testLogger) InfoContext(ctx context.Context, args ...any)  {}
func (testLogger) WarnContext(ctx context.Context, args ...any)  {}
func (testLogger) ErrorContext(ctx context.Context, args ...any) {}
func (testLogger) FatalContext(ctx context.Context, args ...any) {}
func (testLogger) PanicContext(ctx context.Context, args ...any) {}
func (testLogger) Level() int                                    { return 0 }
func (testLogger) SetLevel(level int)                            {}

var _ adapter.V2RayServerTransportHandler = testHandler{}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type failingDialerClient struct {
	err error
}

func (failingDialerClient) IsClosed() bool { return false }
func (failingDialerClient) Close() error   { return nil }

func (c failingDialerClient) OpenStream(context.Context, string, string, io.Reader, bool) (io.ReadCloser, net.Addr, net.Addr, error) {
	return nil, nil, nil, c.err
}

func (c failingDialerClient) PostPacket(context.Context, string, string, string, []byte) error {
	return c.err
}

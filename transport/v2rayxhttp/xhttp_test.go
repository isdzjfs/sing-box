package v2rayxhttp

import (
	"context"
	"io"
	"net"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

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

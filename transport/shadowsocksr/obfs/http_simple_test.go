package obfs

import (
	"io"
	"net"
	"testing"
	"time"
)

func TestHTTPSimpleReadsFragmentedResponseHeader(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	conn := newHTTPSimple(&Base{}).StreamConn(clientConn)

	writeDone := make(chan error, 1)
	go func() {
		if _, err := serverConn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 4\r\n")); err != nil {
			writeDone <- err
			return
		}
		_, err := serverConn.Write([]byte("\r\npong"))
		writeDone <- err
	}()

	payload := make([]byte, 4)
	if _, err := io.ReadFull(conn, payload); err != nil {
		t.Fatal(err)
	}
	if string(payload) != "pong" {
		t.Fatalf("unexpected payload: %q", payload)
	}
	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("fragmented response writer did not finish")
	}
}

func TestTLS12TicketReadsFragmentedServerHandshake(t *testing.T) {
	ticket := newTLS12Ticket(&Base{Key: []byte("test-key")}).(*tls12Ticket)
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	conn := ticket.StreamConn(clientConn).(*tls12TicketConn)
	conn.handshakeStatus = 1

	const handshakeSize = 11 + 32 + 1 + 32
	handshake := make([]byte, handshakeSize)
	copy(handshake[33:43], ticket.hmacSHA1(handshake[11:33]))
	copy(handshake[len(handshake)-10:], ticket.hmacSHA1(handshake[:len(handshake)-10]))
	serverDone := make(chan error, 1)
	go func() {
		if _, err := serverConn.Write(handshake[:19]); err != nil {
			serverDone <- err
			return
		}
		if _, err := serverConn.Write(handshake[19:]); err != nil {
			serverDone <- err
			return
		}
		_ = serverConn.SetReadDeadline(time.Now().Add(time.Second))
		response := make([]byte, 1024)
		_, err := serverConn.Read(response)
		serverDone <- err
	}()

	if _, err := conn.Read(make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	if conn.handshakeStatus != 8 {
		t.Fatalf("unexpected handshake status: %d", conn.handshakeStatus)
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("TLS handshake response was not written")
	}
}

package core

import (
	"crypto/md5"
	"errors"
	"net"
	"sort"
	"strings"

	"github.com/sagernet/sing-box/transport/shadowsocksr/shadowstream"
	N "github.com/sagernet/sing/common/network"
)

type Cipher interface {
	StreamConn(net.Conn) net.Conn
	PacketConn(N.NetPacketConn) N.NetPacketConn
}

var ErrCipherNotSupported = errors.New("cipher not supported")

var streamList = map[string]struct {
	KeySize int
	New     func(key []byte) (shadowstream.Cipher, error)
}{
	"RC4-MD5":       {16, shadowstream.RC4MD5},
	"AES-128-CTR":   {16, shadowstream.AESCTR},
	"AES-192-CTR":   {24, shadowstream.AESCTR},
	"AES-256-CTR":   {32, shadowstream.AESCTR},
	"AES-128-CFB":   {16, shadowstream.AESCFB},
	"AES-192-CFB":   {24, shadowstream.AESCFB},
	"AES-256-CFB":   {32, shadowstream.AESCFB},
	"CHACHA20":      {32, shadowstream.ChaCha20},
	"CHACHA20-IETF": {32, shadowstream.Chacha20IETF},
	"XCHACHA20":     {32, shadowstream.Xchacha20},
}

func ListCipher() []string {
	ciphers := make([]string, 0, len(streamList)+1)
	ciphers = append(ciphers, "DUMMY")
	for cipher := range streamList {
		ciphers = append(ciphers, cipher)
	}
	sort.Strings(ciphers)
	return ciphers
}

func PickCipher(name string, key []byte, password string) (Cipher, error) {
	name = strings.ToUpper(name)
	if name == "DUMMY" {
		return dummy{}, nil
	}
	choice, ok := streamList[name]
	if !ok {
		return nil, ErrCipherNotSupported
	}
	if len(key) == 0 {
		key = Kdf(password, choice.KeySize)
	}
	if len(key) != choice.KeySize {
		return nil, shadowstream.KeySizeError(choice.KeySize)
	}
	cipher, err := choice.New(key)
	if err != nil {
		return nil, err
	}
	return &StreamCipher{Cipher: cipher, Key: key}, nil
}

type StreamCipher struct {
	shadowstream.Cipher
	Key []byte
}

func (c *StreamCipher) StreamConn(conn net.Conn) net.Conn {
	return shadowstream.NewConn(conn, c)
}

func (c *StreamCipher) PacketConn(conn N.NetPacketConn) N.NetPacketConn {
	return shadowstream.NewPacketConn(conn, c)
}

type dummy struct{}

func (dummy) StreamConn(conn net.Conn) net.Conn {
	return conn
}

func (dummy) PacketConn(conn N.NetPacketConn) N.NetPacketConn {
	return conn
}

func Kdf(password string, keyLen int) []byte {
	var b, prev []byte
	h := md5.New()
	for len(b) < keyLen {
		h.Write(prev)
		h.Write([]byte(password))
		b = h.Sum(b)
		prev = b[len(b)-h.Size():]
		h.Reset()
	}
	return b[:keyLen]
}

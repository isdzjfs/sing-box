package shadowsocksr

import (
	"bytes"
	"context"
	"net"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/dialer"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/transport/shadowsocksr/core"
	"github.com/sagernet/sing-box/transport/shadowsocksr/obfs"
	ssrprotocol "github.com/sagernet/sing-box/transport/shadowsocksr/protocol"
	"github.com/sagernet/sing-box/transport/shadowsocksr/shadowstream"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/bufio"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

func RegisterOutbound(registry *outbound.Registry) {
	outbound.Register[option.ShadowsocksROutboundOptions](registry, C.TypeShadowsocksR, NewOutbound)
}

type Outbound struct {
	outbound.Adapter
	logger     logger.ContextLogger
	dialer     N.Dialer
	serverAddr M.Socksaddr
	cipher     core.Cipher
	obfs       obfs.Obfs
	protocol   ssrprotocol.Protocol
}

func NewOutbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.ShadowsocksROutboundOptions) (adapter.Outbound, error) {
	outboundDialer, err := dialer.NewServer(ctx, tag, options.DialerOptions, options.ServerIsDomain())
	if err != nil {
		return nil, err
	}
	method := options.Method
	if method == "" {
		return nil, E.New("shadowsocksr: missing method")
	}
	cipherName := method
	if cipherName == "none" {
		cipherName = "dummy"
	}
	cipher, err := core.PickCipher(cipherName, nil, options.Password)
	if err != nil {
		return nil, E.Cause(err, "shadowsocksr: initialize cipher")
	}
	var (
		ivSize int
		key    []byte
	)
	if cipherName == "dummy" {
		key = core.Kdf(options.Password, 16)
	} else {
		streamCipher, isStreamCipher := cipher.(*core.StreamCipher)
		if !isStreamCipher {
			return nil, E.New(method, " is not none or a supported stream cipher in shadowsocksr")
		}
		ivSize = streamCipher.IVSize()
		key = streamCipher.Key
	}
	obfsName := options.Obfs
	if obfsName == "" {
		obfsName = "plain"
	}
	obfs, obfsOverhead, err := obfs.PickObfs(obfsName, &obfs.Base{
		Host:   options.Server,
		Port:   int(options.ServerPort),
		Key:    key,
		IVSize: ivSize,
		Param:  options.ObfsParam,
	})
	if err != nil {
		return nil, E.Cause(err, "shadowsocksr: initialize obfs")
	}
	protocolName := options.Protocol
	if protocolName == "" {
		protocolName = "origin"
	}
	protocol, err := ssrprotocol.PickProtocol(protocolName, &ssrprotocol.Base{
		Key:      key,
		Overhead: obfsOverhead,
		Param:    options.ProtocolParam,
	})
	if err != nil {
		return nil, E.Cause(err, "shadowsocksr: initialize protocol")
	}
	return &Outbound{
		Adapter:    outbound.NewAdapterWithDialerOptions(C.TypeShadowsocksR, tag, options.Network.Build(), options.DialerOptions),
		logger:     logger,
		dialer:     outboundDialer,
		serverAddr: options.ServerOptions.Build(),
		cipher:     cipher,
		obfs:       obfs,
		protocol:   protocol,
	}, nil
}

func (h *Outbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	ctx, metadata := adapter.ExtendContext(ctx)
	metadata.Outbound = h.Tag()
	metadata.Destination = destination
	switch N.NetworkName(network) {
	case N.NetworkTCP:
		h.logger.InfoContext(ctx, "outbound connection to ", destination)
		conn, err := h.dialer.DialContext(ctx, N.NetworkTCP, h.serverAddr)
		if err != nil {
			return nil, err
		}
		return h.dialTCP(conn, destination)
	case N.NetworkUDP:
		h.logger.InfoContext(ctx, "outbound packet connection to ", destination)
		conn, err := h.listenPacket(ctx)
		if err != nil {
			return nil, err
		}
		return bufio.NewBindPacketConn(conn, destination), nil
	default:
		return nil, E.Extend(N.ErrUnknownNetwork, network)
	}
}

func (h *Outbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	ctx, metadata := adapter.ExtendContext(ctx)
	metadata.Outbound = h.Tag()
	metadata.Destination = destination
	h.logger.InfoContext(ctx, "outbound packet connection to ", destination)
	return h.listenPacket(ctx)
}

func (h *Outbound) dialTCP(conn net.Conn, destination M.Socksaddr) (net.Conn, error) {
	conn = h.obfs.StreamConn(conn)
	conn = h.cipher.StreamConn(conn)
	var writeIV []byte
	if streamConn, isStreamConn := conn.(*shadowstream.Conn); isStreamConn {
		var err error
		writeIV, err = streamConn.ObtainWriteIV()
		if err != nil {
			conn.Close()
			return nil, err
		}
	}
	conn = h.protocol.StreamConn(conn, writeIV)
	if err := M.SocksaddrSerializer.WriteAddrPort(conn, destination); err != nil {
		conn.Close()
		return nil, E.Cause(err, "write request")
	}
	return conn, nil
}

func (h *Outbound) listenPacket(ctx context.Context) (net.PacketConn, error) {
	conn, err := h.dialer.DialContext(ctx, N.NetworkUDP, h.serverAddr)
	if err != nil {
		return nil, err
	}
	packetConn := h.cipher.PacketConn(bufio.NewUnbindPacketConn(conn))
	packetConn = h.protocol.PacketConn(packetConn)
	return &ssrPacketConn{
		NetPacketConn: packetConn,
		serverAddr:    conn.RemoteAddr(),
	}, nil
}

type ssrPacketConn struct {
	N.NetPacketConn
	serverAddr net.Addr
}

func (c *ssrPacketConn) WriteTo(payload []byte, destination net.Addr) (int, error) {
	socksaddr := M.SocksaddrFromNet(destination)
	packet := buf.NewSize(M.SocksaddrSerializer.AddrPortLen(socksaddr) + len(payload))
	defer packet.Release()
	if err := M.SocksaddrSerializer.WriteAddrPort(packet, socksaddr); err != nil {
		return 0, err
	}
	if _, err := packet.Write(payload); err != nil {
		return 0, err
	}
	_, err := c.NetPacketConn.WriteTo(packet.Bytes(), c.serverAddr)
	return len(payload), err
}

func (c *ssrPacketConn) ReadFrom(payload []byte) (int, net.Addr, error) {
	n, _, err := c.NetPacketConn.ReadFrom(payload)
	if err != nil {
		return n, nil, err
	}
	destination, consumed, err := parsePacketDestination(payload[:n])
	if err != nil {
		return 0, nil, err
	}
	copy(payload, payload[consumed:n])
	return n - consumed, destination.UDPAddr(), nil
}

func (c *ssrPacketConn) ReadPacket(buffer *buf.Buffer) (destination M.Socksaddr, err error) {
	_, err = c.NetPacketConn.ReadPacket(buffer)
	if err != nil {
		return
	}
	var consumed int
	destination, consumed, err = parsePacketDestination(buffer.Bytes())
	if err != nil {
		return
	}
	packet := buffer.Bytes()
	copy(packet, packet[consumed:])
	buffer.Truncate(len(packet) - consumed)
	return
}

func (c *ssrPacketConn) WritePacket(buffer *buf.Buffer, destination M.Socksaddr) error {
	defer buffer.Release()
	_, err := c.WriteTo(buffer.Bytes(), destination.UDPAddr())
	return err
}

func parsePacketDestination(packet []byte) (M.Socksaddr, int, error) {
	reader := bytes.NewReader(packet)
	destination, err := M.SocksaddrSerializer.ReadAddrPort(reader)
	if err != nil {
		return M.Socksaddr{}, 0, E.Cause(err, "parse packet address")
	}
	return destination, len(packet) - reader.Len(), nil
}

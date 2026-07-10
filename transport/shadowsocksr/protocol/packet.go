package protocol

import (
	"net"

	"github.com/sagernet/sing-box/transport/shadowsocksr/internal/pool"
	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

type PacketConn struct {
	N.NetPacketConn
	Protocol
}

func (c *PacketConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	buf := pool.GetBuffer()
	defer pool.PutBuffer(buf)
	err := c.EncodePacket(buf, b)
	if err != nil {
		return 0, err
	}
	_, err = c.NetPacketConn.WriteTo(buf.Bytes(), addr)
	return len(b), err
}

func (c *PacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
	n, addr, err := c.NetPacketConn.ReadFrom(b)
	if err != nil {
		return n, addr, err
	}
	decoded, err := c.DecodePacket(b[:n])
	if err != nil {
		return n, addr, err
	}
	copy(b, decoded)
	return len(decoded), addr, nil
}

func (c *PacketConn) ReadPacket(buffer *buf.Buffer) (destination M.Socksaddr, err error) {
	destination, err = c.NetPacketConn.ReadPacket(buffer)
	if err != nil {
		return
	}
	decoded, err := c.DecodePacket(buffer.Bytes())
	if err != nil {
		return
	}
	copy(buffer.Bytes(), decoded)
	buffer.Truncate(len(decoded))
	return
}

func (c *PacketConn) WritePacket(buffer *buf.Buffer, destination M.Socksaddr) error {
	defer buffer.Release()
	buf := pool.GetBuffer()
	defer pool.PutBuffer(buf)
	if err := c.EncodePacket(buf, buffer.Bytes()); err != nil {
		return err
	}
	_, err := c.NetPacketConn.WriteTo(buf.Bytes(), destination.UDPAddr())
	return err
}

package interrupt

import (
	"net"

	"github.com/sagernet/sing/common/bufio"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/common/x/list"
)

type Conn struct {
	net.Conn
	group   *Group
	element *list.Element[*groupConnItem]
	closed  bool
}

func (c *Conn) Close() error {
	c.group.access.Lock()
	if c.element != nil {
		c.group.connections.Remove(c.element)
		c.element = nil
	}
	c.closed = true
	c.group.access.Unlock()
	return c.Conn.Close()
}

func (c *Conn) RegisterGeneration(isExternal bool, generation uint64) {
	c.group.access.Lock()
	defer c.group.access.Unlock()
	if c.closed {
		return
	}
	if c.element == nil {
		c.element = c.group.connections.PushBack(&groupConnItem{c, isExternal, generation})
		return
	}
	c.element.Value.isExternal = isExternal
	c.element.Value.generation = generation
}

func (c *Conn) ReaderReplaceable() bool {
	return true
}

func (c *Conn) WriterReplaceable() bool {
	return true
}

func (c *Conn) Upstream() any {
	return c.Conn
}

type PacketConn struct {
	N.NetPacketConn
	group   *Group
	element *list.Element[*groupConnItem]
	closed  bool
}

func newPacketConn(group *Group, conn net.PacketConn, element *list.Element[*groupConnItem]) *PacketConn {
	return &PacketConn{NetPacketConn: bufio.NewPacketConn(conn), group: group, element: element}
}

func (c *PacketConn) Close() error {
	c.group.access.Lock()
	if c.element != nil {
		c.group.connections.Remove(c.element)
		c.element = nil
	}
	c.closed = true
	c.group.access.Unlock()
	return c.NetPacketConn.Close()
}

func (c *PacketConn) RegisterGeneration(isExternal bool, generation uint64) {
	c.group.access.Lock()
	defer c.group.access.Unlock()
	if c.closed {
		return
	}
	if c.element == nil {
		c.element = c.group.connections.PushBack(&groupConnItem{c, isExternal, generation})
		return
	}
	c.element.Value.isExternal = isExternal
	c.element.Value.generation = generation
}

func (c *PacketConn) ReaderReplaceable() bool {
	return true
}

func (c *PacketConn) WriterReplaceable() bool {
	return true
}

func (c *PacketConn) Upstream() any {
	return c.NetPacketConn
}

type NetworkPacketConn struct {
	N.PacketConn
	group   *Group
	element *list.Element[*groupConnItem]
	closed  bool
}

func (c *NetworkPacketConn) Close() error {
	c.group.access.Lock()
	if c.element != nil {
		c.group.connections.Remove(c.element)
		c.element = nil
	}
	c.closed = true
	c.group.access.Unlock()
	return c.PacketConn.Close()
}

func (c *NetworkPacketConn) RegisterGeneration(isExternal bool, generation uint64) {
	c.group.access.Lock()
	defer c.group.access.Unlock()
	if c.closed {
		return
	}
	if c.element == nil {
		c.element = c.group.connections.PushBack(&groupConnItem{c, isExternal, generation})
		return
	}
	c.element.Value.isExternal = isExternal
	c.element.Value.generation = generation
}

func (c *NetworkPacketConn) ReaderReplaceable() bool {
	return true
}

func (c *NetworkPacketConn) WriterReplaceable() bool {
	return true
}

func (c *NetworkPacketConn) Upstream() any {
	return c.PacketConn
}

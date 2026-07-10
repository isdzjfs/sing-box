package interrupt

import (
	"io"
	"net"
	"sync"

	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/common/x/list"
)

type Group struct {
	access      sync.Mutex
	connections list.List[*groupConnItem]
}

type groupConnItem struct {
	conn       io.Closer
	isExternal bool
	generation uint64
}

func NewGroup() *Group {
	return &Group{}
}

func (g *Group) NewPendingConn(conn net.Conn) *Conn {
	return &Conn{Conn: conn, group: g}
}

func (g *Group) NewConn(conn net.Conn, isExternal bool) net.Conn {
	return g.NewConnWithGeneration(conn, isExternal, 0)
}

func (g *Group) NewConnWithGeneration(conn net.Conn, isExternal bool, generation uint64) net.Conn {
	groupConn := g.NewPendingConn(conn)
	groupConn.RegisterGeneration(isExternal, generation)
	return groupConn
}

func (g *Group) NewPendingPacketConn(conn net.PacketConn) *PacketConn {
	return &PacketConn{PacketConn: conn, group: g}
}

func (g *Group) NewPacketConn(conn net.PacketConn, isExternal bool) net.PacketConn {
	return g.NewPacketConnWithGeneration(conn, isExternal, 0)
}

func (g *Group) NewPacketConnWithGeneration(conn net.PacketConn, isExternal bool, generation uint64) net.PacketConn {
	groupConn := g.NewPendingPacketConn(conn)
	groupConn.RegisterGeneration(isExternal, generation)
	return groupConn
}

func (g *Group) NewPendingNetworkPacketConn(conn N.PacketConn) *NetworkPacketConn {
	return &NetworkPacketConn{PacketConn: conn, group: g}
}

func (g *Group) NewNetworkPacketConn(conn N.PacketConn, isExternal bool) N.PacketConn {
	return g.NewNetworkPacketConnWithGeneration(conn, isExternal, 0)
}

func (g *Group) NewNetworkPacketConnWithGeneration(conn N.PacketConn, isExternal bool, generation uint64) N.PacketConn {
	groupConn := g.NewPendingNetworkPacketConn(conn)
	groupConn.RegisterGeneration(isExternal, generation)
	return groupConn
}

func (g *Group) Interrupt(interruptExternalConnections bool) {
	g.interrupt(0, false, interruptExternalConnections)
}

func (g *Group) InterruptBefore(generation uint64, interruptExternalConnections bool) {
	g.interrupt(generation, true, interruptExternalConnections)
}

func (g *Group) interrupt(generation uint64, filterGeneration bool, interruptExternalConnections bool) {
	g.access.Lock()
	defer g.access.Unlock()
	var toDelete []*list.Element[*groupConnItem]
	for element := g.connections.Front(); element != nil; element = element.Next() {
		item := element.Value
		if (filterGeneration && item.generation >= generation) || (item.isExternal && !interruptExternalConnections) {
			continue
		}
		item.conn.Close()
		toDelete = append(toDelete, element)
	}
	for _, element := range toDelete {
		g.connections.Remove(element)
	}
}

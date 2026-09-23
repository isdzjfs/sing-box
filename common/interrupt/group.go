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

func (g *Group) Add(closer io.Closer, isExternal bool) (remove func()) {
	return g.AddWithGeneration(closer, isExternal, 0)
}

func (g *Group) AddWithGeneration(closer io.Closer, isExternal bool, generation uint64) (remove func()) {
	g.access.Lock()
	element := g.connections.PushBack(&groupConnItem{closer, isExternal, generation})
	g.access.Unlock()
	return func() {
		g.access.Lock()
		g.connections.Remove(element)
		g.access.Unlock()
	}
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
	return newPacketConn(g, conn, nil)
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
	var closers []io.Closer
	for element := g.connections.Front(); element != nil; {
		nextElement := element.Next()
		item := element.Value
		if (!filterGeneration || item.generation < generation) && (!item.isExternal || interruptExternalConnections) {
			closers = append(closers, item.conn)
			g.connections.Remove(element)
		}
		element = nextElement
	}
	g.access.Unlock()
	for _, closer := range closers {
		_ = closer.Close()
	}
}

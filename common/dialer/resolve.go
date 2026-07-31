package dialer

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/common/bufio"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
)

var (
	_ N.Dialer                = (*resolveDialer)(nil)
	_ ParallelInterfaceDialer = (*resolveParallelNetworkDialer)(nil)
)

type ResolveDialer interface {
	N.Dialer
	QueryOptions() adapter.DNSQueryOptions
}

type ParallelInterfaceResolveDialer interface {
	ParallelInterfaceDialer
	QueryOptions() adapter.DNSQueryOptions
}

type resolveDialer struct {
	transport              adapter.DNSTransportManager
	router                 adapter.DNSRouter
	dialer                 N.Dialer
	parallel               bool
	server                 string
	initOnce               sync.Once
	initErr                error
	queryOptions           adapter.DNSQueryOptions
	fallbackDelay          time.Duration
	serverAddressRecorder  *ServerAddressRecorder
	serverAddressRecordTag string
}

func NewResolveDialer(ctx context.Context, dialer N.Dialer, parallel bool, server string, queryOptions adapter.DNSQueryOptions, fallbackDelay time.Duration, serverAddressRecordTag string) ResolveDialer {
	if parallelDialer, isParallel := dialer.(ParallelInterfaceDialer); isParallel {
		return &resolveParallelNetworkDialer{
			resolveDialer{
				transport:              service.FromContext[adapter.DNSTransportManager](ctx),
				router:                 service.FromContext[adapter.DNSRouter](ctx),
				dialer:                 dialer,
				parallel:               parallel,
				server:                 server,
				queryOptions:           queryOptions,
				fallbackDelay:          fallbackDelay,
				serverAddressRecorder:  service.PtrFromContext[ServerAddressRecorder](ctx),
				serverAddressRecordTag: serverAddressRecordTag,
			},
			parallelDialer,
		}
	}
	return &resolveDialer{
		transport:              service.FromContext[adapter.DNSTransportManager](ctx),
		router:                 service.FromContext[adapter.DNSRouter](ctx),
		dialer:                 dialer,
		parallel:               parallel,
		server:                 server,
		queryOptions:           queryOptions,
		fallbackDelay:          fallbackDelay,
		serverAddressRecorder:  service.PtrFromContext[ServerAddressRecorder](ctx),
		serverAddressRecordTag: serverAddressRecordTag,
	}
}

type resolveParallelNetworkDialer struct {
	resolveDialer
	dialer ParallelInterfaceDialer
}

func (d *resolveDialer) initialize() error {
	d.initOnce.Do(d.initServer)
	return d.initErr
}

func (d *resolveDialer) initServer() {
	if d.server == "" {
		return
	}
	transport, loaded := d.transport.Transport(d.server)
	if !loaded {
		d.initErr = E.New("domain resolver not found: " + d.server)
		return
	}
	d.queryOptions.Transport = transport
}

func (d *resolveDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	err := d.initialize()
	if err != nil {
		return nil, err
	}
	if !destination.IsDomain() {
		conn, err := d.dialer.DialContext(ctx, network, destination)
		if err == nil {
			d.recordServerConnection(destination, conn)
		}
		return conn, err
	}
	ctx = log.ContextWithOverrideLevel(ctx, log.LevelDebug)
	addresses, err := d.router.Lookup(ctx, destination.Fqdn, d.queryOptions)
	if err != nil {
		return nil, err
	}
	if d.parallel {
		conn, err := N.DialParallel(ctx, d.dialer, network, destination, addresses, d.queryOptions.Strategy == C.DomainStrategyPreferIPv6, d.fallbackDelay)
		if err == nil && conn != nil {
			d.recordServerConnection(destination, conn)
		}
		return conn, err
	} else {
		conn, err := N.DialSerial(ctx, d.dialer, network, destination, addresses)
		if err == nil && conn != nil {
			d.recordServerConnection(destination, conn)
		}
		return conn, err
	}
}

func (d *resolveDialer) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	err := d.initialize()
	if err != nil {
		return nil, err
	}
	if !destination.IsDomain() {
		conn, err := d.dialer.ListenPacket(ctx, destination)
		if err == nil {
			d.recordServerAddress(destination, destination.Addr)
		}
		return conn, err
	}
	ctx = log.ContextWithOverrideLevel(ctx, log.LevelDebug)
	addresses, err := d.router.Lookup(ctx, destination.Fqdn, d.queryOptions)
	if err != nil {
		return nil, err
	}
	conn, destinationAddress, err := N.ListenSerial(ctx, d.dialer, destination, addresses)
	if err != nil {
		return nil, err
	}
	d.recordServerAddress(destination, destinationAddress)
	return bufio.NewNATPacketConn(bufio.NewPacketConn(conn), M.SocksaddrFrom(destinationAddress, destination.Port), destination), nil
}

func remoteAddressIP(address net.Addr) []byte {
	switch typedAddress := address.(type) {
	case *net.TCPAddr:
		return typedAddress.IP
	case *net.UDPAddr:
		return typedAddress.IP
	default:
		return nil
	}
}

func (d *resolveDialer) recordServerConnection(destination M.Socksaddr, conn net.Conn) {
	if conn == nil || d.serverAddressRecorder == nil || d.serverAddressRecordTag == "" {
		return
	}
	address, ok := netip.AddrFromSlice(remoteAddressIP(conn.RemoteAddr()))
	if ok {
		d.recordServerAddress(destination, address)
	}
}

func (d *resolveDialer) recordServerAddress(destination M.Socksaddr, address netip.Addr) {
	if d.serverAddressRecorder == nil || d.serverAddressRecordTag == "" {
		return
	}
	server := destination.Fqdn
	if server == "" && destination.Addr.IsValid() {
		server = destination.Addr.String()
	}
	d.serverAddressRecorder.Record(d.serverAddressRecordTag, server, destination.Port, address)
}

func (d *resolveDialer) QueryOptions() adapter.DNSQueryOptions {
	return d.queryOptions
}

func (d *resolveDialer) Upstream() any {
	return d.dialer
}

func (d *resolveParallelNetworkDialer) DialParallelInterface(ctx context.Context, network string, destination M.Socksaddr, strategy *C.NetworkStrategy, interfaceType []C.InterfaceType, fallbackInterfaceType []C.InterfaceType, fallbackDelay time.Duration) (net.Conn, error) {
	err := d.initialize()
	if err != nil {
		return nil, err
	}
	if !destination.IsDomain() {
		conn, err := d.dialer.DialContext(ctx, network, destination)
		if err == nil {
			d.recordServerConnection(destination, conn)
		}
		return conn, err
	}
	ctx = log.ContextWithOverrideLevel(ctx, log.LevelDebug)
	addresses, err := d.router.Lookup(ctx, destination.Fqdn, d.queryOptions)
	if err != nil {
		return nil, err
	}
	if fallbackDelay == 0 {
		fallbackDelay = d.fallbackDelay
	}
	if d.parallel {
		conn, err := DialParallelNetwork(ctx, d.dialer, network, destination, addresses, d.queryOptions.Strategy == C.DomainStrategyPreferIPv6, strategy, interfaceType, fallbackInterfaceType, fallbackDelay)
		if err == nil && conn != nil {
			d.recordServerConnection(destination, conn)
		}
		return conn, err
	} else {
		conn, err := DialSerialNetwork(ctx, d.dialer, network, destination, addresses, strategy, interfaceType, fallbackInterfaceType, fallbackDelay)
		if err == nil && conn != nil {
			d.recordServerConnection(destination, conn)
		}
		return conn, err
	}
}

func (d *resolveParallelNetworkDialer) ListenSerialInterfacePacket(ctx context.Context, destination M.Socksaddr, strategy *C.NetworkStrategy, interfaceType []C.InterfaceType, fallbackInterfaceType []C.InterfaceType, fallbackDelay time.Duration) (net.PacketConn, error) {
	err := d.initialize()
	if err != nil {
		return nil, err
	}
	if !destination.IsDomain() {
		conn, err := d.dialer.ListenPacket(ctx, destination)
		if err == nil {
			d.recordServerAddress(destination, destination.Addr)
		}
		return conn, err
	}
	ctx = log.ContextWithOverrideLevel(ctx, log.LevelDebug)
	addresses, err := d.router.Lookup(ctx, destination.Fqdn, d.queryOptions)
	if err != nil {
		return nil, err
	}
	if fallbackDelay == 0 {
		fallbackDelay = d.fallbackDelay
	}
	conn, destinationAddress, err := ListenSerialNetworkPacket(ctx, d.dialer, destination, addresses, strategy, interfaceType, fallbackInterfaceType, fallbackDelay)
	if err != nil {
		return nil, err
	}
	d.recordServerAddress(destination, destinationAddress)
	return bufio.NewNATPacketConn(bufio.NewPacketConn(conn), M.SocksaddrFrom(destinationAddress, destination.Port), destination), nil
}

func (d *resolveParallelNetworkDialer) QueryOptions() adapter.DNSQueryOptions {
	return d.queryOptions
}

func (d *resolveParallelNetworkDialer) Upstream() any {
	return d.dialer
}

package route

import (
	"context"
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	R "github.com/sagernet/sing-box/route/rule"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/stretchr/testify/require"

	"github.com/miekg/dns"
)

func TestActionResolveUsesSniffedUDPDomain(t *testing.T) {
	resolvedAddresses := []netip.Addr{netip.MustParseAddr("203.0.113.10")}
	dnsRouter := &resolveTestDNSRouter{addresses: resolvedAddresses}
	router := newResolveTestRouter(dnsRouter)
	originalDestination := M.SocksaddrFrom(netip.MustParseAddr("198.51.100.1"), 443)
	metadata := adapter.InboundContext{
		Network:     N.NetworkUDP,
		Destination: originalDestination,
		Domain:      "example.com",
	}

	err := router.actionResolve(context.Background(), &metadata, &R.RuleActionResolve{
		Strategy: C.DomainStrategyIPv4Only,
	})

	require.NoError(t, err)
	require.Equal(t, 1, dnsRouter.lookupCount)
	require.Equal(t, "example.com", dnsRouter.domain)
	require.Equal(t, C.DomainStrategyIPv4Only, dnsRouter.options.Strategy)
	require.Equal(t, originalDestination, metadata.Destination)
	require.Equal(t, resolvedAddresses, metadata.DestinationAddresses)
}

func TestActionResolvePrefersDestinationDomain(t *testing.T) {
	dnsRouter := &resolveTestDNSRouter{addresses: []netip.Addr{netip.MustParseAddr("203.0.113.20")}}
	router := newResolveTestRouter(dnsRouter)
	metadata := adapter.InboundContext{
		Network:     N.NetworkUDP,
		Destination: M.ParseSocksaddrHostPort("destination.example", 443),
		Domain:      "sniffed.example",
	}

	err := router.actionResolve(context.Background(), &metadata, &R.RuleActionResolve{})

	require.NoError(t, err)
	require.Equal(t, "destination.example", dnsRouter.domain)
}

func TestActionResolveDoesNotUseSniffedTCPDomain(t *testing.T) {
	dnsRouter := &resolveTestDNSRouter{addresses: []netip.Addr{netip.MustParseAddr("203.0.113.30")}}
	router := newResolveTestRouter(dnsRouter)
	metadata := adapter.InboundContext{
		Network:     N.NetworkTCP,
		Destination: M.SocksaddrFrom(netip.MustParseAddr("198.51.100.1"), 443),
		Domain:      "example.com",
	}

	err := router.actionResolve(context.Background(), &metadata, &R.RuleActionResolve{})

	require.NoError(t, err)
	require.Zero(t, dnsRouter.lookupCount)
	require.Empty(t, metadata.DestinationAddresses)
}

func newResolveTestRouter(dnsRouter *resolveTestDNSRouter) *Router {
	return &Router{
		logger: log.NewNOPFactory().NewLogger("router"),
		dns:    dnsRouter,
	}
}

type resolveTestDNSRouter struct {
	domain      string
	options     adapter.DNSQueryOptions
	addresses   []netip.Addr
	lookupCount int
}

func (r *resolveTestDNSRouter) Start(stage adapter.StartStage) error {
	return nil
}

func (r *resolveTestDNSRouter) Close() error {
	return nil
}

func (r *resolveTestDNSRouter) Exchange(ctx context.Context, message *dns.Msg, options adapter.DNSQueryOptions) (*dns.Msg, error) {
	return nil, nil
}

func (r *resolveTestDNSRouter) Lookup(ctx context.Context, domain string, options adapter.DNSQueryOptions) ([]netip.Addr, error) {
	r.lookupCount++
	r.domain = domain
	r.options = options
	return r.addresses, nil
}

func (r *resolveTestDNSRouter) ClearCache() {
}

func (r *resolveTestDNSRouter) LookupReverseMapping(ip netip.Addr) (string, bool) {
	return "", false
}

func (r *resolveTestDNSRouter) ResetNetwork() {
}

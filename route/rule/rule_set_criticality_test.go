package rule

import (
	"context"
	"net"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
)

type criticalityTestOutbound struct {
	tag          string
	outboundType string
}

func (o *criticalityTestOutbound) Type() string           { return o.outboundType }
func (o *criticalityTestOutbound) Tag() string            { return o.tag }
func (o *criticalityTestOutbound) Network() []string      { return nil }
func (o *criticalityTestOutbound) Dependencies() []string { return nil }

func (o *criticalityTestOutbound) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return nil, nil
}

func (o *criticalityTestOutbound) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, nil
}

type criticalityTestOutboundManager struct {
	outbounds map[string]adapter.Outbound
	final     adapter.Outbound
}

func (m *criticalityTestOutboundManager) Start(adapter.StartStage) error { return nil }
func (m *criticalityTestOutboundManager) Close() error                   { return nil }
func (m *criticalityTestOutboundManager) Outbounds() []adapter.Outbound  { return nil }

func (m *criticalityTestOutboundManager) Outbound(tag string) (adapter.Outbound, bool) {
	outbound, loaded := m.outbounds[tag]
	return outbound, loaded
}

func (m *criticalityTestOutboundManager) Default() adapter.Outbound { return m.final }
func (m *criticalityTestOutboundManager) Remove(string) error       { return nil }

func (m *criticalityTestOutboundManager) Create(context.Context, adapter.Router, log.ContextLogger, string, string, any) error {
	return nil
}

func newCriticalityTestManager(finalType string) *criticalityTestOutboundManager {
	direct := &criticalityTestOutbound{tag: "DIRECT", outboundType: C.TypeDirect}
	block := &criticalityTestOutbound{tag: "REJECT", outboundType: C.TypeBlock}
	proxy := &criticalityTestOutbound{tag: "PROXY", outboundType: C.TypeSelector}
	final := proxy
	if finalType == C.TypeDirect {
		final = direct
	}
	return &criticalityTestOutboundManager{
		outbounds: map[string]adapter.Outbound{"DIRECT": direct, "REJECT": block, "PROXY": proxy},
		final:     final,
	}
}

func routeRuleOn(tags []string, outbound string) adapter.Rule {
	return &DefaultRule{abstractDefaultRule{
		ruleSetItem: &RuleSetItem{tagList: tags},
		action:      &RuleActionRoute{Outbound: outbound},
	}}
}

// A rule-set feeding a direct route is critical: losing it would flip that traffic onto the proxy.
func TestCriticalRuleSetTagsDirectRouteIsCritical(t *testing.T) {
	t.Parallel()
	rules := []adapter.Rule{
		routeRuleOn([]string{"private_domain"}, "DIRECT"),
		routeRuleOn([]string{"cn_ip", "cn_domain"}, "DIRECT"),
	}
	critical, known := CriticalRuleSetTags(rules, newCriticalityTestManager(C.TypeSelector))
	if !known {
		t.Fatal("criticality should be determinable")
	}
	for _, tag := range []string{"private_domain", "cn_ip", "cn_domain"} {
		if !critical[tag] {
			t.Errorf("expected %s to be critical", tag)
		}
	}
	if len(critical) != 3 {
		t.Errorf("unexpected critical set: %v", critical)
	}
}

// Routing to another proxy, or to a block outbound, only degrades precision or ad-blocking.
func TestCriticalRuleSetTagsProxyAndBlockAreOptional(t *testing.T) {
	t.Parallel()
	rules := []adapter.Rule{
		routeRuleOn([]string{"youtube_domain"}, "PROXY"),
		routeRuleOn([]string{"ads_block_domain"}, "REJECT"),
		routeRuleOn([]string{"unknown_target"}, "NOT_DECLARED"),
	}
	critical, known := CriticalRuleSetTags(rules, newCriticalityTestManager(C.TypeSelector))
	if !known {
		t.Fatal("criticality should be determinable")
	}
	if len(critical) != 0 {
		t.Errorf("expected nothing critical, got %v", critical)
	}
}

// With a direct route.final there is no direct-to-proxy flip to protect against.
func TestCriticalRuleSetTagsDirectFinalMakesNothingCritical(t *testing.T) {
	t.Parallel()
	rules := []adapter.Rule{routeRuleOn([]string{"private_domain"}, "DIRECT")}
	critical, known := CriticalRuleSetTags(rules, newCriticalityTestManager(C.TypeDirect))
	if !known {
		t.Fatal("a direct final is still a determined answer")
	}
	if len(critical) != 0 {
		t.Errorf("expected nothing critical with a direct final, got %v", critical)
	}
}

// Tags reached through a logical rule are found too.
func TestCriticalRuleSetTagsThroughLogicalRule(t *testing.T) {
	t.Parallel()
	nested := &DefaultHeadlessRule{abstractDefaultRule{
		ruleSetItem: &RuleSetItem{tagList: []string{"private_ip"}},
	}}
	rules := []adapter.Rule{&LogicalRule{abstractLogicalRule{
		rules:  []adapter.HeadlessRule{nested},
		action: &RuleActionRoute{Outbound: "DIRECT"},
	}}}
	critical, known := CriticalRuleSetTags(rules, newCriticalityTestManager(C.TypeSelector))
	if !known {
		t.Fatal("criticality should be determinable")
	}
	if !critical["private_ip"] {
		t.Errorf("expected private_ip to be critical, got %v", critical)
	}
}

// Without an outbound manager nothing can be judged, so the caller must be told so and fail closed.
func TestCriticalRuleSetTagsNilManagerIsUndetermined(t *testing.T) {
	t.Parallel()
	rules := []adapter.Rule{routeRuleOn([]string{"private_domain"}, "DIRECT")}
	critical, known := CriticalRuleSetTags(rules, nil)
	if known {
		t.Error("criticality must not be reported as known without an outbound manager")
	}
	if len(critical) != 0 {
		t.Errorf("expected empty result, got %v", critical)
	}
}

// An unresolved route.final is likewise undetermined rather than "nothing is critical".
func TestCriticalRuleSetTagsUnresolvedFinalIsUndetermined(t *testing.T) {
	t.Parallel()
	manager := newCriticalityTestManager(C.TypeSelector)
	manager.final = nil
	rules := []adapter.Rule{routeRuleOn([]string{"private_domain"}, "DIRECT")}
	if _, known := CriticalRuleSetTags(rules, manager); known {
		t.Error("criticality must not be reported as known when route.final is unresolved")
	}
}

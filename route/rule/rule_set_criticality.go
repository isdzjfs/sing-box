package rule

import (
	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
)

// ruleSetReferencer is satisfied by every concrete rule type, because they all embed either
// abstractDefaultRule or abstractLogicalRule.
type ruleSetReferencer interface {
	referencedRuleSetTags() []string
}

func (r *abstractDefaultRule) referencedRuleSetTags() []string {
	if r.ruleSetItem == nil {
		return nil
	}
	return r.ruleSetItem.tagList
}

func (r *abstractLogicalRule) referencedRuleSetTags() []string {
	var tags []string
	for _, rule := range r.rules {
		if referencer, isReferencer := rule.(ruleSetReferencer); isReferencer {
			tags = append(tags, referencer.referencedRuleSetTags()...)
		}
	}
	return tags
}

// CriticalRuleSetTags reports the rule-set tags whose absence would change routing in a way the
// config plainly did not intend, and which therefore must not be tolerated as empty.
//
// An empty rule-set never matches, so a rule built on it silently stops firing and its traffic falls
// through to route.final. A rule-set is critical when that fall-through crosses the tunnel boundary
// in either direction:
//
//   - final is a proxy and the rule routes direct: losing the rule-set starts tunnelling
//     private-network and domestic traffic that was meant to stay off the proxy.
//   - final is direct and the rule routes to a proxy: losing the rule-set sends traffic that was
//     meant to be tunnelled out in the clear. This is the shape of a whitelist config, where only
//     the listed destinations are proxied, and it is the more damaging of the two.
//
// Both directions have to be checked. Judging only the first would leave a whitelist config free to
// start with an empty rule-set and quietly stop proxying anything it names.
//
// Losing a rule-set that routes to another proxy while final is also a proxy only changes which
// proxy is used, and losing one that routes to a block outbound only stops something from being
// blocked. Neither crosses the boundary, so neither marks a rule-set critical.
//
// DNS rules are not consulted: their target is a DNS server rather than an outbound, so the flip
// this criterion detects does not apply. In practice the tags that matter (private and domestic
// ranges) are referenced by route rules as well, so they are still covered.
// The second return value is false when criticality could not be established at all. Callers must
// then treat every rule-set as critical: tolerating one whose importance is unknown could silently
// move traffic across the tunnel boundary, which is exactly what this function exists to prevent.
func CriticalRuleSetTags(rules []adapter.Rule, outboundManager adapter.OutboundManager) (map[string]bool, bool) {
	critical := make(map[string]bool)
	if outboundManager == nil {
		return critical, false
	}
	// route.final is what the outbound manager reports as its default; it is the outbound every
	// rule that stops matching falls through to.
	final := outboundManager.Default()
	if final == nil {
		return critical, false
	}
	finalIsDirect := final.Type() == C.TypeDirect
	for _, rule := range rules {
		action, isRoute := rule.Action().(*RuleActionRoute)
		if !isRoute {
			continue
		}
		class, known := classifyOutbound(outboundManager, action.Outbound)
		// An undeclared target routes nowhere, and a block target only stops something from being
		// blocked; neither moves traffic across the tunnel boundary.
		if !known || class == outboundClassBlock {
			continue
		}
		if (class == outboundClassDirect) == finalIsDirect {
			continue
		}
		referencer, isReferencer := rule.(ruleSetReferencer)
		if !isReferencer {
			continue
		}
		for _, tag := range referencer.referencedRuleSetTags() {
			critical[tag] = true
		}
	}
	return critical, true
}

type outboundClass int

const (
	outboundClassDirect outboundClass = iota
	outboundClassBlock
	outboundClassProxy
)

// classifyOutbound reports which side of the tunnel boundary tag sits on. The second return value is
// false for an empty or undeclared tag, which is neither side.
func classifyOutbound(outboundManager adapter.OutboundManager, tag string) (outboundClass, bool) {
	if tag == "" {
		return outboundClassProxy, false
	}
	outbound, loaded := outboundManager.Outbound(tag)
	if !loaded {
		return outboundClassProxy, false
	}
	switch outbound.Type() {
	case C.TypeDirect:
		return outboundClassDirect, true
	case C.TypeBlock:
		return outboundClassBlock, true
	default:
		return outboundClassProxy, true
	}
}

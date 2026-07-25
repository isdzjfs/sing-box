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
// through to route.final. Where such a rule routes to a direct outbound and final is not direct,
// losing the rule-set flips that traffic from direct onto the proxy — private-network and domestic
// traffic would quietly start being tunnelled. Refusing to start is the better outcome there.
//
// Losing a rule-set that routes to another proxy only changes which proxy is used, and losing one
// that routes to a block outbound only stops something from being blocked. Neither is worth
// refusing to start over, so neither marks a rule-set critical.
//
// DNS rules are not consulted: their target is a DNS server rather than an outbound, so the flip
// this criterion detects does not apply. In practice the tags that matter (private and domestic
// ranges) are referenced by route rules as well, so they are still covered.
// The second return value is false when criticality could not be established at all. Callers must
// then treat every rule-set as critical: tolerating one whose importance is unknown could silently
// reroute direct traffic onto the proxy, which is exactly what this function exists to prevent.
func CriticalRuleSetTags(rules []adapter.Rule, outboundManager adapter.OutboundManager) (map[string]bool, bool) {
	critical := make(map[string]bool)
	if outboundManager == nil {
		return critical, false
	}
	final := outboundManager.Default()
	if final == nil {
		return critical, false
	}
	// If traffic already falls through to a direct outbound there is nothing to flip. route.final
	// is what the outbound manager reports as its default.
	if final.Type() == C.TypeDirect {
		return critical, true
	}
	for _, rule := range rules {
		action, isRoute := rule.Action().(*RuleActionRoute)
		if !isRoute || !isDirectOutbound(outboundManager, action.Outbound) {
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

func isDirectOutbound(outboundManager adapter.OutboundManager, tag string) bool {
	if tag == "" {
		return false
	}
	outbound, loaded := outboundManager.Outbound(tag)
	return loaded && outbound.Type() == C.TypeDirect
}

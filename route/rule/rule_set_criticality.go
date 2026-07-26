package rule

import (
	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
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
	finalClass, finalKnown := classifyOutboundInstance(outboundManager, final, make(map[string]bool))
	if !finalKnown || finalClass == outboundClassBlock {
		return critical, false
	}
	finalIsDirect := finalClass == outboundClassDirect
	for _, rule := range rules {
		action, isRoute := rule.Action().(*RuleActionRoute)
		if !isRoute {
			continue
		}
		class, known := classifyOutbound(outboundManager, action.Outbound)
		if !known {
			// Invalid, not-yet-selected, or cyclic groups cannot be assumed to stay on either side
			// of the tunnel boundary. Fail closed for the whole set of rule-sets.
			return critical, false
		}
		// A block target only stops something from being blocked; it does not move traffic across
		// the tunnel boundary.
		if class == outboundClassBlock {
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

// DNSRuleSetTags returns every rule-set referenced by DNS matching. An empty DNS rule-set changes
// which server receives a query and can leak names to the default resolver, so these references are
// always critical rather than being classified through the outbound tunnel boundary.
func DNSRuleSetTags(rules []option.DNSRule) map[string]bool {
	tags := make(map[string]bool)
	var collect func(rule option.DNSRule)
	collect = func(rule option.DNSRule) {
		switch rule.Type {
		case "", C.RuleTypeDefault:
			for _, tag := range rule.DefaultOptions.RuleSet {
				tags[tag] = true
			}
		case C.RuleTypeLogical:
			for _, nestedRule := range rule.LogicalOptions.Rules {
				collect(nestedRule)
			}
		}
	}
	for _, rule := range rules {
		collect(rule)
	}
	return tags
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
	return classifyOutboundInstance(outboundManager, outbound, make(map[string]bool))
}

func classifyOutboundInstance(
	outboundManager adapter.OutboundManager,
	outbound adapter.Outbound,
	visited map[string]bool,
) (outboundClass, bool) {
	if group, isGroup := outbound.(adapter.OutboundGroup); isGroup {
		tag := outbound.Tag()
		if tag != "" {
			if visited[tag] {
				return outboundClassProxy, false
			}
			visited[tag] = true
		}
		selectedTag := group.Now()
		if selectedTag == "" {
			return outboundClassProxy, false
		}
		selected, loaded := outboundManager.Outbound(selectedTag)
		if !loaded {
			return outboundClassProxy, false
		}
		return classifyOutboundInstance(outboundManager, selected, visited)
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

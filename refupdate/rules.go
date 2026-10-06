package refupdate

import (
	"errors"
	"strings"
)

// Wildcard matches exactly one segment within a protected-ref pattern.
const Wildcard = "*"

// Restrictions are the four independent switches a rule may enable.
type Restrictions struct {
	NoDelete      bool
	NoNonFF       bool
	NoCreate      bool
	AllowlistOnly bool
}

// Rule is one protected-ref wildcard rule. Pattern segments are matched
// literally, except the single-segment wildcard "*". For example
// "refs/heads/release/*" matches "refs/heads/release/1.0" but not
// "refs/heads/release/1.0/extra".
type Rule struct {
	Pattern      RefName
	Restrictions Restrictions
	// Allowlist is consulted only when AllowlistOnly is enabled.
	Allowlist []string
}

// effectiveRestrictions is the union of all rules matched by one reference.
type effectiveRestrictions struct {
	noDelete      bool
	noNonFF       bool
	noCreate      bool
	allowlistOnly bool
	allow         map[string]struct{}
}

func (e *effectiveRestrictions) merge(other effectiveRestrictions) {
	e.noDelete = e.noDelete || other.noDelete
	e.noNonFF = e.noNonFF || other.noNonFF
	e.noCreate = e.noCreate || other.noCreate
	e.allowlistOnly = e.allowlistOnly || other.allowlistOnly
	if other.allow != nil {
		if e.allow == nil {
			e.allow = make(map[string]struct{})
		}
		for u := range other.allow {
			e.allow[u] = struct{}{}
		}
	}
}

func (e effectiveRestrictions) allowsUser(user string) bool {
	if !e.allowlistOnly {
		return true
	}
	_, ok := e.allow[user]
	return ok
}

// ruleNode is one segment-trie node.
type ruleNode struct {
	literal  map[string]*ruleNode
	wildcard *ruleNode
	// terminal holds the rule stored at this exact path (one rule per
	// pattern; re-adding a pattern replaces it).
	terminal *effectiveRestrictions

	// probes counts trie nodes touched across all Match calls; it backs the
	// claim that matching cost is independent of the number of rules.
	probes int
}

func newRuleNode() *ruleNode {
	return &ruleNode{literal: make(map[string]*ruleNode)}
}

// RuleSet is an immutable-at-query-time collection of protected-ref rules,
// indexed by a segment trie.
type RuleSet struct {
	root *ruleNode
}

// NewRuleSet builds a RuleSet. Empty pattern segments are rejected.
func NewRuleSet(rules []Rule) (*RuleSet, error) {
	rs := &RuleSet{root: newRuleNode()}
	for _, r := range rules {
		segs := r.Pattern.Segments()
		if len(segs) == 0 {
			return nil, errors.New("refupdate: empty protection pattern")
		}
		for _, seg := range segs {
			if seg == "" {
				return nil, errors.New("refupdate: empty segment in protection pattern " + string(r.Pattern))
			}
		}
		node := rs.root
		for _, seg := range segs {
			if seg == Wildcard {
				if node.wildcard == nil {
					node.wildcard = newRuleNode()
				}
				node = node.wildcard
			} else {
				next := node.literal[seg]
				if next == nil {
					next = newRuleNode()
					node.literal[seg] = next
				}
				node = next
			}
		}
		node.terminal = ruleToEffective(r)
	}
	return rs, nil
}

func ruleToEffective(r Rule) *effectiveRestrictions {
	e := &effectiveRestrictions{
		noDelete:      r.Restrictions.NoDelete,
		noNonFF:       r.Restrictions.NoNonFF,
		noCreate:      r.Restrictions.NoCreate,
		allowlistOnly: r.Restrictions.AllowlistOnly,
	}
	if e.allowlistOnly {
		e.allow = make(map[string]struct{}, len(r.Allowlist))
		for _, u := range r.Allowlist {
			e.allow[u] = struct{}{}
		}
	}
	return e
}

// Match returns the union of restrictions of every rule matching name.
// Each name segment visits at most two trie nodes (literal and wildcard),
// so the number of trie probes is O(segments in the name), never O(#rules).
func (rs *RuleSet) Match(name RefName) effectiveRestrictions {
	var result effectiveRestrictions
	current := []*ruleNode{rs.root}
	for _, seg := range name.Segments() {
		var next []*ruleNode
		for _, node := range current {
			node.probes++
			if child := node.literal[seg]; child != nil {
				next = append(next, child)
			}
			if node.wildcard != nil {
				next = append(next, node.wildcard)
			}
		}
		current = next
	}
	for _, node := range current {
		node.probes++
		if node.terminal != nil {
			result.merge(*node.terminal)
		}
	}
	return result
}

// totalProbes sums probe counters over the whole trie (test support).
func (rs *RuleSet) totalProbes() int {
	total := 0
	var walk func(*ruleNode)
	walk = func(n *ruleNode) {
		total += n.probes
		for _, child := range n.literal {
			walk(child)
		}
		if n.wildcard != nil {
			walk(n.wildcard)
		}
	}
	walk(rs.root)
	return total
}

// patternSegments is exposed for documentation/tests.
func patternSegments(p RefName) []string {
	return strings.Split(string(p), Separator)
}

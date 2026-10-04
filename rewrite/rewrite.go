// Package rewrite implements rewrite rule sets and hop-by-hop path
// rewriting with a hop limit, loop detection and final rules.
package rewrite

import (
	"fmt"

	"ontology/route"
)

// Rule rewrites the longest-prefix match of From to To (re-normalized
// after concatenation). A Final rule stops the rewrite chain.
type Rule struct {
	From  string
	To    string
	Final bool
}

// RuleSet is an immutable set of rewrite rules keyed by From.
type RuleSet struct {
	m *route.Matcher[Rule]
}

// NewRuleSet validates rules (From/To normalized, From distinct) and
// builds a RuleSet. The first violation only is reported.
func NewRuleSet(rules []Rule) (*RuleSet, error) {
	m := route.NewMatcher[Rule]()
	for i, r := range rules {
		if !route.IsNormalized(r.From) {
			return nil, fmt.Errorf("rewrite: rule %d: from %q is not normalized", i, r.From)
		}
		if !route.IsNormalized(r.To) {
			return nil, fmt.Errorf("rewrite: rule %d: to %q is not normalized", i, r.To)
		}
		if !m.Add(r.From, r) {
			return nil, fmt.Errorf("rewrite: rule %d: duplicate from %q", i, r.From)
		}
	}
	return &RuleSet{m: m}, nil
}

// Size returns the number of rules.
func (rs *RuleSet) Size() int { return rs.m.Size() }

// HopLimitError reports that the current path still matched a rule when
// the hop budget K was already exhausted, i.e. a (K+1)-th rewrite would
// be required. It is checked before loop detection.
type HopLimitError struct {
	At   string // current path that still matched a rule
	Hops int    // hops consumed so far (== K)
}

func (e *HopLimitError) Error() string {
	return fmt.Sprintf("rewrite: hop limit exceeded: %q still rewritable after %d hops", e.At, e.Hops)
}

// LoopError reports that a rewrite step produced a path already present
// in the chain.
type LoopError struct {
	Path string // the repeated path
	Step int    // 1-based rewrite step that produced it
}

func (e *LoopError) Error() string {
	return fmt.Sprintf("rewrite: loop: %q reappears at step %d", e.Path, e.Step)
}

// Outcome is the result of a successful rewrite run.
type Outcome struct {
	Final string   // terminal path
	Hops  int      // number of rewrites applied (<= K)
	Chain []string // P0 through Final, no duplicates
}

// Rewrite applies rules hop by hop starting at the normalized path p0,
// with hop budget k. Each step takes the rule whose From is the longest
// prefix of the current path; the next path is Normalize(To + rest).
// The hop-limit check precedes the loop check; a Final rule stops the
// chain after its hop is applied.
func (rs *RuleSet) Rewrite(p0 string, k int) (Outcome, error) {
	chain := []string{p0}
	seen := map[string]struct{}{p0: {}}
	cur := p0
	for {
		rule, rest, ok := rs.m.LongestPrefix(cur)
		if !ok {
			return Outcome{Final: cur, Hops: len(chain) - 1, Chain: chain}, nil
		}
		if len(chain)-1 == k {
			return Outcome{}, &HopLimitError{At: cur, Hops: k}
		}
		next, err := route.Normalize(rule.To + rest)
		if err != nil {
			// Unreachable: To and rest contain only valid bytes and no "..".
			return Outcome{}, fmt.Errorf("rewrite: internal: %w", err)
		}
		if _, dup := seen[next]; dup {
			return Outcome{}, &LoopError{Path: next, Step: len(chain)}
		}
		seen[next] = struct{}{}
		chain = append(chain, next)
		cur = next
		if rule.Final {
			return Outcome{Final: cur, Hops: len(chain) - 1, Chain: chain}, nil
		}
	}
}

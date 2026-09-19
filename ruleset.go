package projection

import (
	"fmt"
	"strings"
)

// Rule is one allow/deny entry. Text is the pattern (e.g. "addr.*").
type Rule struct {
	Text   string
	Effect Effect
}

// Config is a set of visibility rules plus the default effect for fields no
// rule matches. The zero value denies everything by default; pass
// DefaultAllow for a whitelist-free configuration.
type Config struct {
	Allow        []string
	Deny         []string
	DefaultAllow bool
}

// compiledRule stores one validated rule with its 1-based index within its
// side (allow list or deny list), used for error reporting.
type compiledRule struct {
	pattern pattern
	index   int
}

// Ruleset is an immutable, concurrency-safe compiled rule set. Compile once
// and apply it to any number of objects concurrently.
type Ruleset struct {
	allow        []compiledRule
	deny         []compiledRule
	defaultAllow bool

	// exactDenyAncestors indexes exact deny patterns by their segment path,
	// so ancestor overrides can be found by walking a field's prefixes.
	exactDeny map[string]compiledRule
}

// Compile validates and compiles a rule configuration. It rejects invalid
// patterns (reporting the 1-based rule index and original text) and
// configuration conflicts: the exact same pattern appearing in both the
// allow and deny lists is ambiguous and is an error rather than a choice.
func Compile(cfg Config) (*Ruleset, error) {
	rs := &Ruleset{
		defaultAllow: cfg.DefaultAllow,
		exactDeny:    map[string]compiledRule{},
	}
	allowByRaw := map[string]pattern{}
	for i, raw := range cfg.Allow {
		p, err := validatePattern(raw)
		if err != nil {
			return nil, fmt.Errorf("allow rule #%d %q invalid: %w", i+1, raw, err)
		}
		rs.allow = append(rs.allow, compiledRule{pattern: p, index: i + 1})
		allowByRaw[raw] = p
	}
	for i, raw := range cfg.Deny {
		p, err := validatePattern(raw)
		if err != nil {
			return nil, fmt.Errorf("deny rule #%d %q invalid: %w", i+1, raw, err)
		}
		if _, ok := allowByRaw[raw]; ok {
			return nil, fmt.Errorf("configuration conflict: pattern %q appears in both allow and deny", raw)
		}
		rs.deny = append(rs.deny, compiledRule{pattern: p, index: i + 1})
		if !p.wildcard {
			rs.exactDeny[strings.Join(p.segments, ".")] = compiledRule{pattern: p, index: i + 1}
		}
	}
	return rs, nil
}

// decision resolves the visibility of the field at path.
//
// Precedence, highest first:
//  1. an exact deny rule on an ancestor (cascades to all descendants);
//  2. an explicit rule matching the field directly, where the more specific
//     pattern wins (exact beats single-level wildcard), and ties deny wins;
//  3. the rule set default effect.
func (rs *Ruleset) decision(path []string) Decision {
	d := Decision{Path: strings.Join(path, ".")}
	if cr, ok := rs.ancestorDeny(path); ok {
		d.Visible = false
		d.Effect = EffectDeny
		d.Reason = ReasonAncestorOverride
		d.RuleRaw = cr.pattern.raw
		d.OverriddenBy = cr.pattern.raw
		return d
	}
	if cr, eff, ok := rs.directMatch(path); ok {
		d.Effect = eff
		d.Reason = ReasonDirectRule
		d.RuleRaw = cr.pattern.raw
		d.Visible = eff == EffectAllow
		return d
	}
	d.Reason = ReasonDefault
	d.Visible = rs.defaultAllow
	if rs.defaultAllow {
		d.Effect = EffectAllow
	} else {
		d.Effect = EffectDeny
	}
	return d
}

// ancestorDeny returns the nearest exact deny rule whose path is a strict
// prefix of path. Exact denials cascade through every descendant.
func (rs *Ruleset) ancestorDeny(path []string) (compiledRule, bool) {
	var nearest compiledRule
	found := false
	for n := 1; n < len(path); n++ {
		key := strings.Join(path[:n], ".")
		if cr, ok := rs.exactDeny[key]; ok {
			nearest = cr
			found = true
		}
	}
	return nearest, found
}

// directMatch finds the winning explicit rule for the field itself. The most
// specific matching pattern wins; at equal specificity deny wins.
func (rs *Ruleset) directMatch(path []string) (compiledRule, Effect, bool) {
	var bestAllow, bestDeny *compiledRule
	bestAllowSpec, bestDenySpec := 0, 0
	for i := range rs.allow {
		cr := &rs.allow[i]
		if cr.pattern.matchesDirect(path) && cr.pattern.specificity() > bestAllowSpec {
			bestAllow = cr
			bestAllowSpec = cr.pattern.specificity()
		}
	}
	for i := range rs.deny {
		cr := &rs.deny[i]
		if cr.pattern.matchesDirect(path) && cr.pattern.specificity() > bestDenySpec {
			bestDeny = cr
			bestDenySpec = cr.pattern.specificity()
		}
	}
	switch {
	case bestDeny != nil && bestAllow != nil:
		if bestDenySpec >= bestAllowSpec {
			return *bestDeny, EffectDeny, true
		}
		return *bestAllow, EffectAllow, true
	case bestDeny != nil:
		return *bestDeny, EffectDeny, true
	case bestAllow != nil:
		return *bestAllow, EffectAllow, true
	default:
		return compiledRule{}, 0, false
	}
}

// Explain reports why the named dotted field is visible or hidden, including
// the original text of the rule that decided it.
func (rs *Ruleset) Explain(dottedPath string) Decision {
	path := strings.Split(dottedPath, ".")
	return rs.decision(path)
}

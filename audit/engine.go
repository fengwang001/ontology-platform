package audit

import "sort"

// Engine adjudicates one access request against one concrete rule set.
// Implementations must be pure functions of (rules, request): replay
// correctness requires that the same inputs always yield the same output.
type Engine interface {
	Tag() string
	Adjudicate(rules RuleSet, req AccessRequest) bool
}

// CanonicalEngine is the single deterministic adjudication used for every
// replay. It sorts rules deterministically (Order then ID) and applies
// first-match-wins with a default-deny fallback.
type CanonicalEngine struct{}

// Tag returns the canonical engine identifier.
func (CanonicalEngine) Tag() string { return canonicalEngineTag }

const canonicalEngineTag = "canonical:v1"

// Adjudicate returns true iff the first matching rule after deterministic
// ordering is an ALLOW rule. No matching rule denies.
func (CanonicalEngine) Adjudicate(rules RuleSet, req AccessRequest) bool {
	ordered := append([]Rule(nil), rules.Rules...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Order != ordered[j].Order {
			return ordered[i].Order < ordered[j].Order
		}
		return ordered[i].ID < ordered[j].ID
	})
	for _, r := range ordered {
		if member(r.Subjects, req.Subject) && member(r.Targets, req.Target) && member(r.Actions, req.Action) {
			return r.Effect == EffectAllow
		}
	}
	return false
}

// member reports whether value is in set. An empty set is the wildcard.
func member(set []string, value string) bool {
	if len(set) == 0 {
		return true
	}
	for _, v := range set {
		if v == value {
			return true
		}
	}
	return false
}

// InvertedEngine is a deliberately defective engine used to create historical
// audit records whose original decision differs from a canonical replay.
type InvertedEngine struct{}

// Tag returns the defective engine identifier.
func (InvertedEngine) Tag() string { return "inverted:v1" }

// Adjudicate returns the opposite of the canonical decision.
func (InvertedEngine) Adjudicate(rules RuleSet, req AccessRequest) bool {
	return !(CanonicalEngine{}).Adjudicate(rules, req)
}

// StaticEngine returns a fixed answer regardless of input. It models
// historical engines with arbitrary deterministic defects in differential
// tests; its Tag is caller supplied.
type StaticEngine struct {
	Name   string
	Answer bool
}

// Tag returns the engine name.
func (e StaticEngine) Tag() string {
	if e.Name == "" {
		return "static:v1"
	}
	return e.Name
}

// Adjudicate returns the fixed answer.
func (e StaticEngine) Adjudicate(_ RuleSet, _ AccessRequest) bool { return e.Answer }

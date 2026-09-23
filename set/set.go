// Package set holds ordered include/exclude wildcard rules with last-match-wins
// semantics and atomic hot replacement.
package set

import (
	"sync/atomic"

	"ontology/engine"
	"ontology/syntax"
)

// Verdict values.
const (
	Undecided = 0
	Included  = 1
	Excluded  = 2
)

// Rule is one compiled include/exclude rule.
type Rule struct {
	Text     string
	Exclude  bool
	Index    int
	Version  uint64
	matcher  *engine.Matcher
}

type version struct {
	rules   []*Rule
	version uint64
}

// Set is a concurrency-safe rule set.
type Set struct {
	cur atomic.Pointer[version]
	lim syntax.Limits
	maxRules int
}

// New creates an empty set using lim for compilation limits.
func New(lim syntax.Limits, maxRules int) *Set {
	s := &Set{lim: lim}
	s.cur.Store(&version{})
	s.maxRules = maxRules
	return s
}

// Decide returns the verdict for path under one consistent version.
func (s *Set) Decide(path string) (verdict int) {
	v := s.cur.Load()
	for _, r := range v.rules {
		if r.matcher.Match(path) {
			if r.Exclude {
				verdict = Excluded
			} else {
				verdict = Included
			}
		}
	}
	return verdict
}

// Explain returns the last rule deciding path and its version. The second
// result is false when no rule matches (undecided).
func (s *Set) Explain(path string) (Rule, uint64, bool) {
	v := s.cur.Load()
	var hit *Rule
	for _, r := range v.rules {
		if r.matcher.Match(path) {
			hit = r
		}
	}
	if hit == nil {
		return Rule{}, v.version, false
	}
	return *hit, v.version, true
}

// Replace atomically installs ruleTexts ('!' prefix means exclude). Any
// compile failure leaves the currently installed version untouched.
func (s *Set) Replace(ruleTexts []string) error {
	if s.maxRules > 0 && len(ruleTexts) > s.maxRules {
		return ErrTooManyRules
	}
	rules := make([]*Rule, len(ruleTexts))
	for i, text := range ruleTexts {
		body := text
		exclude := false
		if len(text) > 0 && text[0] == '!' {
			body, exclude = text[1:], true
		}
		m, err := engine.Compile(body, s.lim)
		if err != nil {
			return &RuleError{Index: i, Text: text, Err: err}
		}
		rules[i] = &Rule{Text: text, Exclude: exclude, Index: i, matcher: m}
	}
	old := s.cur.Load()
	nv := &version{rules: rules, version: old.version + 1}
	for _, r := range rules {
		r.Version = nv.version
	}
	s.cur.Store(nv)
	return nil
}

// ErrTooManyRules is returned when a replacement exceeds the rule cap.
var ErrTooManyRules = setErr("too many rules")

// RuleError wraps a compile failure for one rule during replacement.
type RuleError struct {
	Index int
	Text  string
	Err   error
}

func (e *RuleError) Error() string { return e.Err.Error() }
func (e *RuleError) Unwrap() error { return e.Err }

type setErr string

func (e setErr) Error() string { return string(e) }

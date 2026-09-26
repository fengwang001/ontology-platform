// Package api is the public face of the matcher: compile a pattern
// once, then match texts against it from any number of goroutines.
package api

import (
	"errors"
	"fmt"

	"ontology/match"
	"ontology/parse"
)

// Re-exported sentinels so callers only import this package.
var (
	ErrSyntax      = parse.ErrSyntax
	ErrUnsupported = parse.ErrUnsupported
	ErrTooLong     = parse.ErrTooLong
)

// Matcher is a compiled, immutable pattern. Safe for concurrent use.
type Matcher struct {
	pattern string
}

// Compile validates pattern and fixes it. On error nothing is
// stored anywhere: the failure leaves no trace.
func Compile(pattern string) (*Matcher, error) {
	if err := parse.Validate(pattern); err != nil {
		return nil, err
	}
	return &Matcher{pattern: pattern}, nil
}

// Match reports whether the compiled pattern matches the whole of
// s. An over-long text is rejected before any evaluation, so the
// rejection cannot alter any state.
func (m *Matcher) Match(s string) (bool, error) {
	if len(s) > parse.MaxLen {
		return false, ErrTooLong
	}
	return match.Match(s, m.pattern), nil
}

// SelfCheck verifies the four invariants on built-in cases and
// returns a descriptive error on the first violation found.
func SelfCheck() error {
	// Invariants 1+2: memoized DP agrees with naive backtracking.
	pairs := []struct {
		s, p string
		want bool
	}{
		{"aab", "c*a*b", true},
		{"a", "a.", false},
		{"mississippi", "mis*is*p*.", false},
		{"ab", ".*", true},
		{"", "a*b*", true},
		{"", ".*", true},
		{"", ".", false},
		{"aa", "a", false},
		{"aaa", "a*a", true},
		{"abcd", "d*", false},
	}
	for _, c := range pairs {
		if got := match.Match(c.s, c.p); got != c.want || got != match.Naive(c.s, c.p) {
			return fmt.Errorf("selfcheck: (%q,%q) got %v want %v", c.s, c.p, got, c.want)
		}
	}
	// Invariant 4: rejections are distinguishable and leave no trace.
	m, err := Compile("a*b")
	if err != nil {
		return fmt.Errorf("selfcheck: compile good pattern: %w", err)
	}
	before, _ := m.Match("aaab")
	var e1, e2, e3, e4 error
	if _, e1 = Compile("*a"); !errors.Is(e1, ErrSyntax) {
		return fmt.Errorf("selfcheck: leading * gave %v", e1)
	}
	if _, e2 = Compile("a**"); !errors.Is(e2, ErrSyntax) {
		return fmt.Errorf("selfcheck: ** gave %v", e2)
	}
	if _, e3 = Compile("a b"); !errors.Is(e3, ErrUnsupported) {
		return fmt.Errorf("selfcheck: bad char gave %v", e3)
	}
	long := make([]byte, parse.MaxLen+1)
	if _, e4 = m.Match(string(long)); !errors.Is(e4, ErrTooLong) {
		return fmt.Errorf("selfcheck: long text gave %v", e4)
	}
	if errors.Is(e1, e3) || errors.Is(e3, e4) || errors.Is(e1, e4) {
		return errors.New("selfcheck: sentinel errors not distinct")
	}
	if after, _ := m.Match("aaab"); after != before {
		return errors.New("selfcheck: state changed after rejections")
	}
	return nil
}

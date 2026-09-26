// Package api is the public entry point to the wildcard matcher. It depends
// only on match (which in turn depends on parse); the dependency direction is
// one-way. A Compiled pattern is immutable after construction, so Match is
// safe for concurrent use.
package api

import (
	"errors"
	"fmt"
	"strings"

	"ontology/match"
	"ontology/parse"
)

// Decidable sentinel errors, re-exported from parse so callers have a single
// import root. The three rejection causes are pairwise distinct.
var (
	ErrUnsupportedChar  = parse.ErrUnsupportedChar
	ErrInputTooLong     = parse.ErrInputTooLong
	ErrTooManyWildcards = parse.ErrTooManyWildcards
	// ErrSelfCheck wraps any failure reported by SelfCheck.
	ErrSelfCheck = errors.New("api: self-check failed")
)

// Compiled is an immutable, concurrency-safe compiled pattern.
type Compiled struct {
	pat *match.Pattern
}

// Compile fully validates pattern before constructing anything. On rejection
// it returns (nil, err) and creates no object, so no observable state can be
// left behind by a failed call.
func Compile(pattern string) (*Compiled, error) {
	q, err := match.Compile(pattern)
	if err != nil {
		return nil, err
	}
	return &Compiled{pat: q}, nil
}

// Match reports whether the whole text s is matched. An over-long text is
// rejected (false, ErrInputTooLong) before any matching state is touched;
// the receiver itself is immutable.
func (c *Compiled) Match(s string) (bool, error) {
	if err := parse.ValidateText(s); err != nil {
		return false, err
	}
	return c.pat.Match(s), nil
}

// specPairs are the built-in pairs exercising all four invariants:
// greedy backtracking, exact '?'/'*' semantics, and trailing-*' swallowing.
var specPairs = []struct {
	s, p string
	want bool
}{
	{"adceb", "*a*b", true},
	{"aab", "*ab", true},
	{"ab", "a?b", false},
	{"ab", "a*", true},
	{"acdcb", "a*c?b", false},
	{"", "*", true}, {"", "**", true}, {"", "?", false}, {"", "", true},
	{"aab", "????", false}, {"abc", "???", true},
	{"xxxxb", "*a*b", false}, {"aaabab", "*ab", true},
}

var linearSizes = []int{100, 316, 1000, 3162, 10000}

// SelfCheck verifies the four invariants over built-in pairs:
// equality with naive DP, greedy backtracking completeness, exact wildcard
// semantics, and that every rejection is decidable and leaves no trace. It
// also re-checks the linear pointer-advance bound. It returns nil when all
// checks hold.
func SelfCheck() error {
	for _, c := range specPairs {
		q, err := Compile(c.p)
		if err != nil {
			return fail("compile %q: %v", c.p, err)
		}
		got, err := q.Match(c.s)
		if err != nil {
			return fail("match (%q,%q): %v", c.s, c.p, err)
		}
		if got != c.want {
			return fail("(%q,%q)=%v want %v", c.s, c.p, got, c.want)
		}
		if !match.AgreesWithNaive(c.s, c.p) {
			return fail("naive-DP disagreement on (%q,%q)", c.s, c.p)
		}
	}
	// The three rejection causes are distinct and each is decidable.
	if _, err := Compile("a\x01b"); !errors.Is(err, ErrUnsupportedChar) {
		return fail("unsupported-char not rejected: %v", err)
	}
	if _, err := Compile(strings.Repeat("a", parse.MaxLen+1)); !errors.Is(err, ErrInputTooLong) {
		return fail("over-long pattern not rejected: %v", err)
	}
	if _, err := Compile(strings.Repeat("*", parse.MaxWildcards+1)); !errors.Is(err, ErrTooManyWildcards) {
		return fail("wildcard limit not rejected: %v", err)
	}
	c, err := Compile("*")
	if err != nil {
		return fail("compile after rejection: %v", err)
	}
	if ok, err := c.Match("still-works"); err != nil || !ok {
		return fail("normal matching after rejection: ok=%v err=%v", ok, err)
	}
	if ok, err := c.Match(strings.Repeat("a", parse.MaxLen+1)); !errors.Is(err, ErrInputTooLong) || ok {
		return fail("over-long text not rejected: ok=%v err=%v", ok, err)
	}
	if ok, _ := c.Match("after"); !ok {
		return fail("matching not restored after text rejection")
	}
	if !match.LinearAdvance("*a*a?b", linearSizes) {
		return fail("pointer advances exceed linear bound")
	}
	return nil
}

func fail(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrSelfCheck, fmt.Sprintf(format, args...))
}

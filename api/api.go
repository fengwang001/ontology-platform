// Package api is the public facade for string period detection.
package api

import (
	"errors"
	"fmt"

	"ontology/period"
	"ontology/pfx"
)

// Sentinel errors: each rejection is decidable via errors.Is.
var (
	ErrEmpty     = errors.New("api: empty string")
	ErrTooLong   = errors.New("api: string exceeds maxLen")
	ErrBadPeriod = errors.New("api: period out of range")
)

const maxLen = 1 << 20

// Checker answers period queries about one fixed string. Immutable after
// New, so all methods are safe for concurrent use.
type Checker struct {
	s    string
	pf   *pfx.PrefixFunc
	minP int
}

// New builds a Checker for s. Rejects empty or over-long strings without
// retaining any state.
func New(s string) (*Checker, error) {
	if len(s) == 0 {
		return nil, ErrEmpty
	}
	if len(s) > maxLen {
		return nil, ErrTooLong
	}
	pf := pfx.Build(s)
	return &Checker{s: s, pf: pf, minP: len(s) - pf.LongestBorder()}, nil
}

// MinPeriod returns the smallest period of the string.
func (c *Checker) MinPeriod() (int, error) { return c.minP, nil }

// IsPeriodic reports whether p is a period; p outside [1, len] is rejected.
func (c *Checker) IsPeriodic(p int) (bool, error) {
	if p < 1 || p > len(c.s) {
		return false, ErrBadPeriod
	}
	return period.IsPeriodic(c.s, p), nil
}

// IsPower reports whether the string is a k>=2 repetition of a proper prefix.
func (c *Checker) IsPower() bool {
	n := len(c.s)
	return c.minP < n && n%c.minP == 0
}

// SelfCheck verifies the four invariants on built-in strings. It returns a
// descriptive error on the first violation, nil when all hold.
func (c *Checker) SelfCheck() error {
	for _, s := range []string{"ababab", "abababa", "abcabcabc", "abcde", "a", "aaaa"} {
		if err := checkOne(s); err != nil {
			return err
		}
	}
	return nil
}

// checkOne verifies one string against naive definitions.
func checkOne(s string) error {
	n := len(s)
	naiveMin := 0
	for p := 1; p <= n && naiveMin == 0; p++ {
		if period.IsPeriodic(s, p) {
			naiveMin = p
		}
	}
	if got := period.MinPeriod(s); got != naiveMin {
		return fmt.Errorf("selfcheck %q: MinPeriod=%d, naive=%d", s, got, naiveMin)
	}
	pf := pfx.Build(s)
	for p := 1; p <= n; p++ {
		if period.IsPeriodic(s, p) != (p == n || pf.IsBorder(n-p)) {
			return fmt.Errorf("selfcheck %q: period/border duality broken at p=%d", s, p)
		}
	}
	power := false
	for p := 1; p < n; p++ {
		if n%p == 0 && period.IsPeriodic(s, p) {
			power = true
		}
	}
	if period.IsPower(s) != power {
		return fmt.Errorf("selfcheck %q: IsPower mismatch", s)
	}
	return nil
}

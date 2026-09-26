// Package api is the public entry point for string-period queries on one
// fixed, in-memory string.
package api

import (
	"errors"
	"strings"

	"ontology/period"
	"ontology/pfx"
)

// maxLen bounds the accepted string length.
const maxLen = 1 << 20

// Distinct, decidable sentinel errors for the three rejection cases.
var (
	ErrEmptyString      = errors.New("api: string must not be empty")
	ErrStringTooLong    = errors.New("api: string length exceeds maxLen")
	ErrPeriodOutOfRange = errors.New("api: period must satisfy 1 <= p <= len(s)")
)

// String is a fixed byte string; it is immutable after New succeeds, so all
// query methods are safe for concurrent use by multiple goroutines.
type String struct {
	s string
}

// New fixes the string. It fails atomically (returns nil, allocates no usable
// object) on an empty or over-long input.
func New(s string) (*String, error) {
	if len(s) == 0 {
		return nil, ErrEmptyString
	}
	if len(s) > maxLen {
		return nil, ErrStringTooLong
	}
	return &String{s: s}, nil
}

// MinPeriod returns the smallest period of the fixed string.
func (x *String) MinPeriod() (int, error) {
	return period.MinPeriod(x.s), nil
}

// IsPeriodic reports whether p is a period. An out-of-range p is a rejected
// operation: it returns the sentinel and changes no state.
func (x *String) IsPeriodic(p int) (bool, error) {
	if p <= 0 || p > len(x.s) {
		return false, ErrPeriodOutOfRange
	}
	return period.IsPeriodic(x.s, p), nil
}

// IsPower reports whether the string is t^k for some k >= 2.
func (x *String) IsPower() bool { return period.IsPower(x.s) }

// SelfCheck verifies the four invariants on a built-in set of strings.
func (x *String) SelfCheck() bool {
	specimens := []string{
		"a", "ab", "ababab", "abababa", "abcabcabc", "abcabcab",
		"abcde", "aaaa", "aaaaa", "abcabcd",
	}
	for _, s := range specimens {
		y, err := New(s)
		if err != nil {
			return false
		}
		n := len(s)
		// (1) agreement with the naive definition, for every p.
		mp, _ := y.MinPeriod()
		if mp != naiveMinPeriod(s) {
			return false
		}
		// (2) period p  <=>  border n-p; (3) power iff p<n and n%p==0.
		for p := 1; p <= n; p++ {
			got, err := y.IsPeriodic(p)
			if err != nil || got != borderEq(s, n-p) {
				return false
			}
		}
		powerBySearch := false
		for p := 1; p < n; p++ {
			if n%p == 0 && period.IsPeriodic(s, p) {
				powerBySearch = true
			}
		}
		if y.IsPower() != powerBySearch {
			return false
		}
	}
	// (4) rejected operations leave no trace and are distinct errors.
	if _, err := New(""); !errors.Is(err, ErrEmptyString) {
		return false
	}
	if _, err := New(strings.Repeat("a", maxLen+1)); !errors.Is(err, ErrStringTooLong) {
		return false
	}
	z, _ := New("ababab")
	if _, err := z.IsPeriodic(0); !errors.Is(err, ErrPeriodOutOfRange) {
		return false
	}
	if ok, _ := z.IsPeriodic(2); !ok || z.IsPower() != true { // instance still usable
		return false
	}
	return pfx.VerifyLinear()
}

func naiveMinPeriod(s string) int {
	for p := 1; p <= len(s); p++ {
		if period.IsPeriodic(s, p) {
			return p
		}
	}
	return 0
}

func borderEq(s string, b int) bool {
	return b == 0 || s[:b] == s[len(s)-b:]
}

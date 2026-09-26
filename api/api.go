// Package api is the public face of the Boyer–Moore searcher: validated
// construction, stateful search, and a built-in self-check.
package api

import (
	"bytes"
	"errors"
	"fmt"
	"slices"

	"ontology/bm"
	"ontology/shift"
)

var (
	ErrEmptyPattern   = errors.New("api: empty pattern")
	ErrPatternTooLong = errors.New("api: pattern exceeds maxPatLen")
	ErrTooManyMatches = errors.New("api: cumulative matches exceed maxMatches")
)

const (
	maxPatLen  = 4096
	maxMatches = 1 << 16
)

// Matcher searches one precomputed pattern and remembers the last result.
type Matcher struct {
	s       *bm.Searcher
	matches []int
	total   int // cumulative matches accepted so far
}

// New validates pattern and precomputes both shift tables.
func New(pattern []byte) (*Matcher, error) {
	if len(pattern) == 0 {
		return nil, ErrEmptyPattern
	}
	if len(pattern) > maxPatLen {
		return nil, ErrPatternTooLong
	}
	return &Matcher{s: bm.New(pattern)}, nil
}

// Search finds all occurrences in text. On rejection (cumulative match
// count would exceed maxMatches) nothing changes and the error is
// distinguishable via errors.Is.
func (m *Matcher) Search(text []byte) ([]int, error) {
	res, over := m.s.Search(text, maxMatches-m.total)
	if over {
		return nil, ErrTooManyMatches
	}
	m.matches = res
	m.total += len(res)
	return res, nil
}

// Matches returns a copy of the last accepted search result.
func (m *Matcher) Matches() []int {
	return slices.Clone(m.matches)
}

// naive is the reference: compare the pattern at every start position.
func naive(text, pat []byte) []int {
	var out []int
	for i := 0; i+len(pat) <= len(text); i++ {
		if bytes.Equal(text[i:i+len(pat)], pat) {
			out = append(out, i)
		}
	}
	return out
}

// bruteGS is the independent oracle for gs[k]: the smallest shift d that no
// text consistent with the known mismatch can invalidate.
func bruteGS(p []byte, k int) int {
	m := len(p)
	for d := 1; d < m; d++ {
		ok := true
		for t := max(k, d); t < m && ok; t++ {
			ok = p[t-d] == p[t]
		}
		if ok && k >= 1 && k-1-d >= 0 && p[k-1-d] == p[k-1] {
			ok = false
		}
		if ok {
			return d
		}
	}
	return m
}

// SelfCheck verifies the four invariants on built-in texts. It only uses
// fresh temporary matchers, so it never touches the receiver's state and is
// safe for concurrent use.
func (m *Matcher) SelfCheck() error {
	pats := []string{"abab", "aaaab", "aa", "a", "abcdefghij", "abacaba"}
	texts := []string{"abababcab", "aaaaab", "aaaa", "", "z", "ababab", "abacabacaba"}
	for _, p := range pats { // invariants 2 & 3: shift tables are safe
		pb := []byte(p)
		last := shift.BadChar(pb)
		for c := 0; c < 256; c++ {
			want := -1
			for i, b := range pb {
				if b == byte(c) {
					want = i
				}
			}
			if last[c] != want {
				return fmt.Errorf("bad-char table mismatch for %q", p)
			}
		}
		gs := shift.GoodSuffix(pb)
		for k := 0; k <= len(pb); k++ {
			if gs[k] != bruteGS(pb, k) {
				return fmt.Errorf("good-suffix table mismatch for %q k=%d", p, k)
			}
		}
	}
	for _, p := range pats { // invariant 1: identical to naive search
		mm, err := New([]byte(p))
		if err != nil {
			return err
		}
		for _, t := range texts {
			got, err := mm.Search([]byte(t))
			if err != nil || !slices.Equal(got, naive([]byte(t), []byte(p))) {
				return fmt.Errorf("mismatch with naive for %q in %q", p, t)
			}
		}
	}
	if _, err := New(nil); !errors.Is(err, ErrEmptyPattern) { // invariant 4
		return errors.New("empty pattern not rejected")
	}
	if _, err := New(make([]byte, maxPatLen+1)); !errors.Is(err, ErrPatternTooLong) {
		return errors.New("overlong pattern not rejected")
	}
	mm, _ := New([]byte("aa"))
	if _, err := mm.Search([]byte("aaaa")); err != nil {
		return err
	}
	before := mm.Matches()
	if _, err := mm.Search(bytes.Repeat([]byte("a"), maxMatches+2)); !errors.Is(err, ErrTooManyMatches) {
		return errors.New("match overflow not rejected")
	}
	if !slices.Equal(mm.Matches(), before) {
		return errors.New("rejected search changed state")
	}
	return nil
}

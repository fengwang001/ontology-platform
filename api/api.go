// Package api is the public face of the Aho-Corasick matcher.
package api

import (
	"errors"
	"fmt"
	"sync"
	"unicode/utf8"

	"ontology/ac"
	"ontology/trie"
)

// Sentinel errors; every rejection is decidable via errors.Is.
var (
	ErrEmptyPatterns = errors.New("api: empty pattern set")
	ErrEmptyPattern  = errors.New("api: empty pattern")
	ErrInvalidUTF8   = errors.New("api: invalid utf-8")
	ErrNotBuilt      = errors.New("api: matcher not built")
)

// UTF8Error reports invalid UTF-8; Offset is the first bad byte.
type UTF8Error struct{ Offset int }

func (e *UTF8Error) Error() string     { return fmt.Sprintf("%v at byte %d", ErrInvalidUTF8, e.Offset) }
func (e *UTF8Error) Is(err error) bool { return err == ErrInvalidUTF8 }

// badUTF8 returns the offset of the first invalid byte, or -1.
func badUTF8(s string) int {
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			return i
		}
		i += size
	}
	return -1
}

// build validates everything first; a rejection changes no state.
func build(patterns []string) (*ac.Automaton, error) {
	if len(patterns) == 0 {
		return nil, ErrEmptyPatterns
	}
	for i, p := range patterns {
		if p == "" {
			return nil, fmt.Errorf("%w at index %d", ErrEmptyPattern, i)
		}
		if off := badUTF8(p); off >= 0 {
			return nil, &UTF8Error{Offset: off}
		}
	}
	tr := trie.New()
	for i, p := range patterns {
		tr.Insert(p, i)
	}
	tr.Build()
	return ac.NewAutomaton(tr), nil
}

var mu sync.RWMutex
var cur *ac.Automaton

// New validates patterns and, only on success, swaps in the matcher.
func New(patterns []string) error {
	a, err := build(patterns)
	if err != nil {
		return err
	}
	mu.Lock()
	cur = a
	mu.Unlock()
	return nil
}

// Match reports all occurrences of all patterns in text.
func Match(text string) ([]ac.Match, error) {
	mu.RLock()
	a := cur
	mu.RUnlock()
	if a == nil {
		return nil, ErrNotBuilt
	}
	if off := badUTF8(text); off >= 0 {
		return nil, &UTF8Error{Offset: off}
	}
	return a.Match(text), nil
}

// naive is the reference: every pattern, every start, byte by byte.
func naive(pats []string, text string) (out []ac.Match) {
	for pi, p := range pats {
		for s := 0; s+len(p) <= len(text); s++ {
			if text[s:s+len(p)] == p {
				out = append(out, ac.Match{Pattern: pi, End: s + len(p)})
			}
		}
	}
	return
}

// sameMultiset reports whether a and b hold the same (pattern, End) pairs.
func sameMultiset(a, b []ac.Match) bool {
	if len(a) != len(b) {
		return false
	}
	cnt := map[ac.Match]int{}
	for _, m := range a {
		cnt[m]++
	}
	for _, m := range b {
		if cnt[m]--; cnt[m] < 0 {
			return false
		}
	}
	return true
}

// SelfCheck verifies the four invariants on the built-in section-3 case.
func SelfCheck() error {
	pats, text := []string{"a", "ab", "bab"}, "abab"
	a, err := build(pats)
	if err != nil {
		return err
	}
	got := a.Match(text)
	want := []ac.Match{{Pattern: 0, End: 1}, {Pattern: 1, End: 2}, {Pattern: 0, End: 3}, {Pattern: 2, End: 4}, {Pattern: 1, End: 4}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		return fmt.Errorf("selfcheck: got %v want %v", got, want)
	}
	if !sameMultiset(got, naive(pats, text)) { // invariants 1+2
		return errors.New("selfcheck: mismatch vs naive reference")
	}
	for _, mt := range got { // invariant 3
		p := pats[mt.Pattern]
		if mt.End < len(p) || mt.End > len(text) || text[mt.End-len(p):mt.End] != p {
			return fmt.Errorf("selfcheck: bad position %+v", mt)
		}
	}
	rejects := []struct {
		pats []string
		sent error
	}{{nil, ErrEmptyPatterns}, {[]string{"x", ""}, ErrEmptyPattern}, {[]string{"\xff"}, ErrInvalidUTF8}}
	for _, r := range rejects { // invariant 4
		if _, err := build(r.pats); !errors.Is(err, r.sent) {
			return fmt.Errorf("selfcheck: %v not rejected", r.sent)
		}
	}
	return nil
}

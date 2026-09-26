// Package api is the public face of the streaming KMP matcher.
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/match"
	"ontology/pfx"
)

// Limits enforced by New and Feed.
const (
	MaxPatLen  = 1 << 20
	MaxMatches = 1 << 20
)

// Distinguishable sentinel errors for every rejectable failure.
var (
	ErrEmptyPattern   = errors.New("kmp: empty pattern")
	ErrPatternTooLong = errors.New("kmp: pattern too long")
	ErrTooManyMatches = match.ErrTooManyMatches
)

// Matcher is a concurrency-safe streaming KMP matcher.
type Matcher struct {
	mu sync.RWMutex
	m  *match.Matcher
}

// New validates the pattern and returns a ready Matcher.
func New(pattern []byte) (*Matcher, error) {
	if len(pattern) == 0 {
		return nil, ErrEmptyPattern
	}
	if len(pattern) > MaxPatLen {
		return nil, ErrPatternTooLong
	}
	return &Matcher{m: match.New(pattern, MaxMatches)}, nil
}

// Feed consumes one chunk and returns the new absolute end indices.
// A rejected chunk changes no state.
func (x *Matcher) Feed(chunk []byte) ([]int, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.m.Feed(chunk)
}

// Matches returns a copy of all end indices collected so far.
func (x *Matcher) Matches() []int {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return x.m.Hits()
}

// naive collects overlapping end indices by brute force.
func naive(p, t []byte) []int {
	var out []int
	for s := 0; s+len(p) <= len(t); s++ {
		i := 0
		for i < len(p) && t[s+i] == p[i] {
			i++
		}
		if i == len(p) {
			out = append(out, s+len(p)-1)
		}
	}
	return out
}

func equal(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// SelfCheck verifies the four invariants on built-in sequences. It
// touches no receiver state and is safe for concurrent use.
func (x *Matcher) SelfCheck() error {
	pats := []string{"abaaba", "aa", "a", "abcabc"}
	texts := []string{"aabaabaab", "aaaa", "abababa", "abcabcabc"}
	for _, p := range pats {
		pi := pfx.Compute([]byte(p))
		for i := range p { // invariant 2: pi[i] is the longest proper prefix==suffix
			want := 0
			for l := 1; l <= i; l++ {
				if p[:l] == p[i+1-l:i+1] {
					want = l
				}
			}
			if pi[i] != want {
				return fmt.Errorf("selfcheck: pi[%d]=%d want %d", i, pi[i], want)
			}
		}
		for _, t := range texts { // invariant 1: streaming equals naive
			m := match.New([]byte(p), MaxMatches)
			for k := 0; k < len(t); k += 3 {
				if _, err := m.Feed([]byte(t[k:min(k+3, len(t))])); err != nil {
					return err
				}
			}
			if !equal(m.Hits(), naive([]byte(p), []byte(t))) {
				return fmt.Errorf("selfcheck: %q@%q != naive", p, t)
			}
		}
	}
	// Invariant 3: comparisons stay within 2*(m+n) on a built-in text.
	p, t := []byte("aabaab"), []byte(texts[2]+texts[3])
	cmps, j, pi := 0, 0, pfx.Compute(p)
	for _, c := range t {
		j = pfx.Step(p, pi, j, c, &cmps)
		if j == len(p) {
			j = pi[len(pi)-1]
		}
	}
	if cmps > 2*(len(t)+len(p)) {
		return fmt.Errorf("selfcheck: cmps=%d exceeds bound", cmps)
	}
	// Invariant 4: a rejected batch leaves every state field untouched.
	m := match.New([]byte("aa"), 2)
	if _, err := m.Feed([]byte("aaaa")); !errors.Is(err, ErrTooManyMatches) {
		return fmt.Errorf("selfcheck: want ErrTooManyMatches, got %v", err)
	}
	if len(m.Hits()) != 0 {
		return errors.New("selfcheck: rejected feed left hits")
	}
	if _, err := m.Feed([]byte("aa")); err != nil || !equal(m.Hits(), []int{1}) {
		return errors.New("selfcheck: matcher unusable after rejection")
	}
	return nil
}

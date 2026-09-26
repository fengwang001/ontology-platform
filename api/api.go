// Package api is the public entry point: it validates the pattern,
// precomputes both jump tables and keeps cumulative match state in
// process memory. It depends on bm (and thereby on shift).
package api

import (
	"errors"
	"strings"
	"sync"

	"ontology/bm"
)

const (
	maxPatLen  = 1 << 20
	maxMatches = 1 << 10
)

// Distinguishable sentinel errors; reject with errors.Is.
var (
	ErrEmptyPattern   = errors.New("api: empty pattern")
	ErrPatternTooLong = errors.New("api: pattern longer than maxPatLen")
	ErrTooManyMatches = errors.New("api: cumulative match count exceeds maxMatches")
)

// Matcher holds one validated pattern and the cumulative matches.
type Matcher struct {
	eng *bm.Matcher

	mu      sync.RWMutex
	matches []int // cumulative start indices of every accepted Search
	total   int
}

// New validates pattern and precomputes the tables. A rejected New
// constructs nothing, so there is no state to leave behind.
func New(pattern []byte) (*Matcher, error) {
	if len(pattern) == 0 {
		return nil, ErrEmptyPattern
	}
	if len(pattern) > maxPatLen {
		return nil, ErrPatternTooLong
	}
	return &Matcher{eng: bm.New(string(pattern))}, nil
}

// Search reports all start positions. If appending the hits would push
// the cumulative count past maxMatches the call fails wholesale and no
// state changes; the instance stays usable afterwards.
func (m *Matcher) Search(text []byte) ([]int, error) {
	hits := m.eng.Search(string(text))
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.total+len(hits) > maxMatches {
		return nil, ErrTooManyMatches
	}
	m.total += len(hits)
	m.matches = append(m.matches, hits...)
	return append([]int(nil), hits...), nil
}

// Matches returns a copy of every match accepted so far. Concurrency-safe.
func (m *Matcher) Matches() []int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]int(nil), m.matches...)
}

var selfCheckCorpus = []struct{ text, pat string }{
	{"abababcab", "abab"}, {"aaaaab", "aaaab"}, {"aaaa", "aa"},
	{"ababab", "abab"}, {"", "x"}, {"xyz", "xyz"},
	{"aaaaa", "a"}, {"abababab", "aba"}, {"mississippi", "issi"},
}

func naiveHits(text, pat string) []int {
	var hits []int
	for s := 0; s+len(pat) <= len(text); s++ {
		ok := true
		for k := 0; k < len(pat); k++ {
			if text[s+k] != pat[k] {
				ok = false
				break
			}
		}
		if ok {
			hits = append(hits, s)
		}
	}
	return hits
}

func eqInts(a, b []int) bool {
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

// SelfCheck verifies the four invariants on built-in texts. Safe for
// concurrent use: it builds only local matchers and mutates only those.
func (m *Matcher) SelfCheck() bool {
	for _, c := range selfCheckCorpus {
		if !eqInts(bm.Search(c.text, c.pat), naiveHits(c.text, c.pat)) {
			return false // invariants 1-3: naive equality plus both safe jumps
		}
	}
	if !eqInts(bm.Search("aaaaab", "aaaab"), []int{1}) {
		return false // (甲): bad-character jump must not skip s=1
	}
	if !eqInts(bm.Search("aaaa", "aa"), []int{0, 1, 2}) ||
		!eqInts(bm.Search("ababab", "abab"), []int{0, 2}) {
		return false // (乙)(丙): period/full-match and good-suffix shifts
	}
	if _, err := New(nil); !errors.Is(err, ErrEmptyPattern) {
		return false
	}
	if _, err := New(make([]byte, maxPatLen+1)); !errors.Is(err, ErrPatternTooLong) {
		return false
	}
	q, err := New([]byte("a"))
	if err != nil {
		return false
	}
	if _, err := q.Search([]byte("aa")); err != nil {
		return false
	}
	before := q.Matches()
	if _, err := q.Search([]byte(strings.Repeat("a", maxMatches))); !errors.Is(err, ErrTooManyMatches) || !eqInts(q.Matches(), before) {
		return false // invariant 4: rejection leaves no trace
	}
	if r, err := q.Search([]byte("a")); err != nil || !eqInts(q.Matches(), []int{0, 1, 0}) {
		_ = r
		return false // still usable after the rejection
	}
	return true
}

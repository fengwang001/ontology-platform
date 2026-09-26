// Package bm runs the Boyer–Moore search loop: right-to-left comparison
// and a shift of max(bad-character, good-suffix). It depends on shift.
package bm

import (
	"strings"

	"ontology/shift"
)

// Matcher is a pattern with its tables precomputed. The comparison
// counter is intentionally unexported and never leaves the package as a
// number; white-box tests in this package read it directly.
type Matcher struct {
	pat string
	tab shift.Table
	cmp int // character pairs compared during the latest Search
}

// New precomputes the jump tables for pat.
func New(pat string) *Matcher {
	return &Matcher{pat: pat, tab: shift.Build(pat)}
}

// Search returns every 0-based start index of the pattern in text,
// overlaps included, comparing the window from right to left.
func (m *Matcher) Search(text string) []int {
	m.cmp = 0
	p, n := m.pat, len(text)
	ml := len(p)
	var hits []int
	if ml == 0 {
		return hits
	}
	for s := 0; s <= n-ml; {
		j := ml - 1
		for j >= 0 {
			m.cmp++
			if p[j] != text[s+j] {
				break
			}
			j--
		}
		if j < 0 { // complete match: shift by the least period
			hits = append(hits, s)
			s += m.tab.Good[0]
			continue
		}
		bc := j - m.tab.Last[text[s+j]] // bad-character jump
		if bc < 1 {
			bc = 1
		}
		sh := bc
		if g := m.tab.Good[j+1]; g > sh { // good-suffix jump
			sh = g
		}
		s += sh
	}
	return hits
}

// Search is the package-level entry point required by the task: build the
// tables for pat and report all start positions in text.
func Search(text, pat string) []int {
	return New(pat).Search(text)
}

// SublinearCheck reports only a verdict (never the counter value): with a
// pattern of m distinct bytes whose last byte is absent from the text,
// comparison counts stay within n/m+m for several text lengths n.
func SublinearCheck() bool {
	pat := "abcdefghij" // m=10, trailing 'j' never occurs in the text
	m := len(pat)
	for _, n := range []int{100, 500, 1000, 5000, 10000} {
		e := New(pat)
		e.Search(strings.Repeat("z", n))
		if e.cmp > n/m+m {
			return false
		}
	}
	return true
}

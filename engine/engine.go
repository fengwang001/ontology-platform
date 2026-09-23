// Package engine executes a compiled syntax pattern against a path.
package engine

import (
	"strings"

	"ontology/runes"
	"ontology/syntax"
)

// Matcher matches paths against one compiled pattern.
type Matcher struct {
	pat   *syntax.Pattern
	steps int
}

// New compiles pattern with default limits.
func New(pattern string) (*Matcher, error) {
	p, err := syntax.Compile(pattern, syntax.DefaultLimits)
	if err != nil {
		return nil, err
	}
	return &Matcher{pat: p}, nil
}

// NewWithLimits compiles pattern under lim.
func NewWithLimits(pattern string, lim syntax.Limits) (*Matcher, error) {
	p, err := syntax.Compile(pattern, lim)
	if err != nil {
		return nil, err
	}
	return &Matcher{pat: p}, nil
}

// Pattern returns the original pattern text.
func (m *Matcher) Pattern() string { return m.pat.Raw }

// AtomCount returns the number of within-segment atoms.
func (m *Matcher) AtomCount() int { return m.pat.AtomCount }

// Match reports whether path matches the pattern and records comparison steps.
func (m *Matcher) Match(path string) bool {
	m.steps = 0
	ps := strings.Split(path, "/")
	dp := make([]bool, len(ps)+1)
	dp[0] = true
	for _, seg := range m.pat.Segments {
		next := make([]bool, len(ps)+1)
		if seg.DoubleStar {
			for j := 0; j <= len(ps); j++ {
				if dp[j] {
					next[j] = true // consume zero segments
					for k := j; k < len(ps) && ps[k] != ""; k++ {
						m.steps++
						next[k+1] = true // consume non-empty segment
					}
				}
			}
		} else {
			for j := 0; j < len(ps); j++ {
				if dp[j] && m.matchSeg(seg, ps[j]) {
					next[j+1] = true
				}
			}
		}
		dp = next
	}
	return dp[len(ps)]
}

// Steps returns the number of basic comparison steps in the last Match.
func (m *Matcher) Steps() int { return m.steps }

// matchSeg matches one pattern segment against one path segment using the
// single-star rollback algorithm (no exponential backtracking).
func (m *Matcher) matchSeg(seg syntax.Segment, s string) bool {
	atoms, text := seg.Atoms, runes.Items(s)
	p, q := 0, 0
	starP, starQ := -1, -1
	for q <= len(text) {
		if p == len(atoms) {
			if q == len(text) {
				return true
			}
			if starP >= 0 { // let the last star eat one more code point
				p, q = starP+1, starQ+1
				starQ++
				continue
			}
			return false
		}
		a := atoms[p]
		if a.Kind == syntax.AStar {
			starP, starQ, p = p, q, p+1
			continue
		}
		if q < len(text) && m.atomMatch(a, text[q]) {
			p, q = p+1, q+1
			continue
		}
		if starP >= 0 {
			p, q = starP+1, starQ+1
			starQ++
			continue
		}
		return false
	}
	return false
}

func (m *Matcher) atomMatch(a syntax.Atom, it runes.Item) bool {
	m.steps++
	switch a.Kind {
	case syntax.ALiteral:
		return runes.Eq(a.Lit, it)
	case syntax.AQuestion:
		return it.R != '/'
	case syntax.AClass:
		return a.Class.Match(it)
	}
	return false
}

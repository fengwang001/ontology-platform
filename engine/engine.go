// Package engine executes compiled patterns against byte-string paths.
package engine

import (
	"ontology/runes"
	"ontology/syntax"
)

// Matcher is a compiled pattern ready to match.
type Matcher struct {
	p     *syntax.Pattern
	steps int64
}

// Compile compiles pattern with the given syntax limits.
func Compile(pattern string, lim syntax.Limits) (*Matcher, error) {
	p, err := syntax.Compile(pattern, lim)
	if err != nil {
		return nil, err
	}
	return &Matcher{p: p}, nil
}

// Steps reports basic comparison steps used by the most recent Match call.
func (m *Matcher) Steps() int64 { return m.steps }

// Match reports whether path matches the pattern.
func (m *Matcher) Match(path string) bool {
	m.steps = 0
	segs := splitSegments(path)
	ps := m.p.Segments
	if !m.hasStarStar() {
		if len(ps) != len(segs) {
			return false
		}
		for i, s := range ps {
			if !m.matchSegment(s.Atoms, segs[i]) {
				return false
			}
		}
		return true
	}
	n := len(ps)
	k := len(segs)
	dp := make([]uint8, (n+1)*(k+1))
	at := func(i, j int) *uint8 { return &dp[i*(k+1)+j] }
	*at(0, 0) = 1
	for i := 0; i < n; i++ {
		for j := 0; j <= k; j++ {
			if *at(i, j) != 1 {
				continue
			}
			if ps[i].DoubleStar {
				*at(i+1, j) = 1 // consume zero segments
				if j < k {
					*at(i, j+1) = 1 // consume one more segment
				}
			} else if j < k && m.matchSegment(ps[i].Atoms, segs[j]) {
				*at(i+1, j+1) = 1
			}
		}
	}
	return *at(n, k) == 1
}

func (m *Matcher) hasStarStar() bool {
	for _, s := range m.p.Segments {
		if s.DoubleStar {
			return true
		}
	}
	return false
}

// matchSegment uses the last-star fallback algorithm: linear, no exponential
// backtracking.
func (m *Matcher) matchSegment(atoms []syntax.Atom, seg []runes.Rune) bool {
	i, j := 0, 0
	starI, starJ := -1, -1
	for j < len(seg) {
		switch {
		case i < len(atoms) && atoms[i].Kind == syntax.AStar:
			starI, starJ = i, j
			i++
		case i < len(atoms) && m.atomMatch(atoms[i], seg[j]):
			i++
			j++
		case starI >= 0:
			i = starI + 1
			starJ++
			j = starJ
		default:
			return false
		}
	}
	for i < len(atoms) && atoms[i].Kind == syntax.AStar {
		i++
	}
	return i == len(atoms)
}

func (m *Matcher) atomMatch(a syntax.Atom, r runes.Rune) bool {
	m.steps++
	switch a.Kind {
	case syntax.AAny, syntax.AStar:
		return true
	case syntax.ALiteral:
		return runes.Eq(a.Lit, r)
	case syntax.AClass:
		return a.Cls.Match(r)
}
	return false
}

func splitSegments(path string) [][]runes.Rune {
	if path == "" {
		return nil
	}
	var segs [][]runes.Rune
	start := 0
	for i := 0; i < len(path); i++ {
		if path[i] == '/' {
			segs = append(segs, runes.Slice(path[start:i]))
			start = i + 1
		}
	}
	segs = append(segs, runes.Slice(path[start:]))
	return segs
}

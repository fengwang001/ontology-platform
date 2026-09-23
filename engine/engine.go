// Package engine executes compiled patterns against paths.
package engine

import (
	"strings"
	"sync/atomic"

	"ontology/runes"
	"ontology/syntax"
)

// Pattern is a compiled, executable pattern.
type Pattern struct {
	p     *syntax.Pattern
	steps atomic.Int64 // basic comparison steps of the last Match
}

// Compile compiles pat; on error no usable Pattern is returned.
func Compile(pat string, lim syntax.Limits) (*Pattern, error) {
	sp, err := syntax.Compile(pat, lim)
	if err != nil {
		return nil, err
	}
	return &Pattern{p: sp}, nil
}

// Steps returns the comparison steps of the most recent Match.
func (p *Pattern) Steps() int64 { return p.steps.Load() }

// Atoms returns the pattern's atom count (atoms plus ** segments).
func (p *Pattern) Atoms() int { return p.p.NAtom }

// Match reports whether path matches the pattern. It runs a
// segment-level NFA state set, so time is O(atoms x path runes).
func (p *Pattern) Match(path string) bool {
	p.steps.Store(0)
	segs := p.p.Segs
	n := len(segs)
	active := make([]bool, n+1)
	active[0] = true
	closeOver(active, segs)
	for _, ps := range strings.Split(path, "/") {
		next := make([]bool, n+1)
		for i := 0; i < n; i++ {
			if !active[i] {
				continue
			}
			if segs[i].Double {
				next[i] = true
			} else if p.matchSeg(segs[i].Atoms, ps) {
				next[i+1] = true
			}
		}
		active = next
		closeOver(active, segs)
	}
	return active[n]
}

// closeOver adds i+1 for every active bare-** segment (zero segments).
func closeOver(active []bool, segs []syntax.Seg) {
	for i := 0; i+1 < len(active); i++ {
		if active[i] && segs[i].Double {
			active[i+1] = true
		}
	}
}

// matchSeg matches one path segment linearly, remembering only the
// last '*' as backtrack point.
func (p *Pattern) matchSeg(atoms []syntax.Atom, s string) bool {
	star, starS := -1, 0
	pi, si := 0, 0
	for si < len(s) {
		if pi < len(atoms) && atoms[pi].Kind == syntax.Star {
			star, starS = pi, si
			pi++
			continue
		}
		if pi < len(atoms) {
			p.steps.Add(1)
			if atomMatch(atoms[pi], s, si) {
				_, _, si = runes.Step(s, si)
				pi++
				continue
			}
		}
		if star < 0 {
			return false
		}
		pi = star + 1
		_, _, starS = runes.Step(s, starS)
		si = starS
	}
	for pi < len(atoms) && atoms[pi].Kind == syntax.Star {
		pi++
	}
	return pi == len(atoms)
}

func atomMatch(a syntax.Atom, s string, i int) bool {
	r, raw, _ := runes.Step(s, i)
	switch a.Kind {
	case syntax.Lit:
		return raw == a.Raw
	case syntax.Any:
		return true
	case syntax.Cls:
		return a.Cls.Match(r)
	}
	return false
}

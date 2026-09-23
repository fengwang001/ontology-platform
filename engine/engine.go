// Package engine executes compiled patterns against paths with an
// O(pattern atoms x path runes) guarantee: single-star fallback inside
// segments, a segment-level NFA subset construction across segments.
package engine

import (
	"strings"
	"sync/atomic"

	"ontology/runes"
	"ontology/syntax"
)

var steps atomic.Int64 // basic comparisons of the most recent Match

// LastSteps returns the comparison count of the most recent Match call.
func LastSteps() int64 { return steps.Load() }

// Match reports whether path matches the compiled pattern p.
func Match(p *syntax.Pattern, path string) bool {
	var n int64
	ok := nfa(p, strings.Split(path, "/"), &n)
	steps.Store(n)
	return ok
}

// nfa runs the segment-level subset construction. State i means "the
// next pattern segment to consume is Segs[i]"; state len(Segs) accepts.
func nfa(p *syntax.Pattern, segs []string, n *int64) bool {
	cur := map[int]bool{0: true}
	closure(p, cur)
	for _, seg := range segs {
		next := map[int]bool{}
		for i := range cur {
			if i >= len(p.Segs) {
				continue
			}
			*n++
			s := p.Segs[i]
			switch {
			case s.Glob:
				next[i] = true // ** consumes this segment and stays
			case segMatch(s.Atoms, seg, n):
				next[i+1] = true
			}
		}
		closure(p, next)
		cur = next
	}
	return cur[len(p.Segs)]
}

// closure adds i+1 for every active globstar state i (zero segments).
func closure(p *syntax.Pattern, st map[int]bool) {
	for i := 0; i < len(p.Segs); i++ {
		if st[i] && p.Segs[i].Glob {
			st[i+1] = true
		}
	}
}

// segMatch matches atoms against one whole segment (no '/' inside)
// using last-star fallback; retries never exceed the rune count.
func segMatch(atoms []syntax.Atom, text string, n *int64) bool {
	pi, ti := 0, 0
	sp, st := -1, 0 // last star: pattern index, text offset
	for ti < len(text) {
		*n++
		if pi < len(atoms) && atoms[pi].Kind != syntax.Star {
			if ok, w := atomAt(atoms[pi], text, ti); ok {
				pi, ti = pi+1, ti+w
				continue
			}
		}
		if pi < len(atoms) && atoms[pi].Kind == syntax.Star {
			sp, st = pi, ti
			pi++
		} else if sp >= 0 {
			_, size := runes.Decode([]byte(text), st)
			st += size // let the last star swallow one more rune
			pi, ti = sp+1, st
		} else {
			return false
		}
	}
	for pi < len(atoms) && atoms[pi].Kind == syntax.Star {
		pi++
	}
	return pi == len(atoms)
}

// atomAt tests one non-star atom at text offset ti; ok implies w > 0.
func atomAt(a syntax.Atom, text string, ti int) (ok bool, w int) {
	switch a.Kind {
	case syntax.Lit:
		if strings.HasPrefix(text[ti:], a.Lit) {
			return true, len(a.Lit)
		}
		return false, 0
	case syntax.Any:
		_, size := runes.Decode([]byte(text), ti)
		return true, size
	default: // syntax.Cls
		r, size := runes.Decode([]byte(text), ti)
		return a.Cl.Match(r), size
	}
}

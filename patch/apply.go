package patch

import (
	"ontology/hunk"
	"ontology/lines"
)

type pat struct {
	kind hunk.Kind
	line lines.Line
}

func pattern(h hunk.Hunk) []pat {
	var ps []pat
	for _, r := range h.Rows {
		if r.Kind == hunk.Ctx || r.Kind == hunk.Old {
			ps = append(ps, pat{r.Kind, r.Line})
		}
	}
	return ps
}

func matchAt(cur []lines.Line, pos int, ps []pat) bool {
	j := pos
	for _, p := range ps {
		if j >= len(cur) || !cur[j].Equal(p.line) {
			return false
		}
		j++
	}
	return true
}

// locate returns the match index and its delta from the shifted recorded
// start. Nearest match wins; equal distance favors the earlier position.
func locate(cur []lines.Line, h hunk.Hunk, oldStart, shift, fuzz int) (int, int, Reason) {
	ps := pattern(h)
	center := oldStart - 1 + shift
	if h.OldCount == 0 {
		center = oldStart + shift
	}
	for off := 0; off <= fuzz; off++ {
		for _, cand := range []int{center - off, center + off} {
			if cand >= 0 && cand <= len(cur) && matchAt(cur, cand, ps) {
				return cand, cand - center, 0
			}
		}
	}
	for cand := 0; cand <= len(cur); cand++ {
		if matchAt(cur, cand, ps) {
			return 0, 0, ROffset
		}
	}
	return 0, 0, RContext
}

// splice replaces the matched old span with Ctx and New rows.
func splice(cur []lines.Line, pos int, h hunk.Hunk) []lines.Line {
	var repl []lines.Line
	consumed := 0
	for _, r := range h.Rows {
		if r.Kind == hunk.Mark {
			continue
		}
		if r.Kind == hunk.Old {
			consumed++
			continue
		}
		repl = append(repl, r.Line)
		if r.Kind == hunk.Ctx {
			consumed++
		}
	}
	out := append([]lines.Line{}, cur[:pos]...)
	out = append(out, repl...)
	out = append(out, cur[pos+consumed:]...)
	return out
}

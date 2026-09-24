// Package match implements LIKE-style wildcard matching with a
// single backtracking anchor: linear steps, no recursion.
package match

import (
	"ontology/budget"
	"ontology/pattern"
)

// Match reports whether text matches p. Every scan iteration is
// counted on m (nil disables counting); if m has a limit and it is
// exceeded, Match aborts with budget.ErrExhausted.
func Match(p pattern.Pattern, text string, m *budget.Meter) (bool, error) {
	if err := pattern.CheckUTF8("text", text); err != nil {
		return false, err
	}
	runes := []rune(text)
	toks := p.Tokens()
	n, nt := len(runes), len(toks)
	i, j := 0, 0
	starJ, starI := -1, -1
	for i < n || j < nt {
		if m != nil && !m.Step() {
			return false, budget.ErrExhausted
		}
		switch {
		case j < nt && toks[j].Kind == pattern.AnyMany:
			starJ, starI = j, i
			j++
		case j < nt && i < n &&
			(toks[j].Kind == pattern.AnyOne || toks[j].Lit == runes[i]):
			i++
			j++
		case starJ >= 0 && starI < n:
			j = starJ + 1
			starI++
			i = starI
		default:
			return false, nil
		}
	}
	return true, nil
}

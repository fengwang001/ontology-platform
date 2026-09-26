// Package match implements greedy wildcard matching with backtracking.
// It depends only on parse.
package match

import (
	"strings"

	"ontology/parse"
)

// Pattern is an immutable compiled pattern; concurrent Match calls on a
// Pattern are race-free because all per-call state lives in a local engine.
type Pattern struct{ r []rune }

// Compile validates pattern through parse and prepares it for matching.
func Compile(p string) (*Pattern, error) {
	if err := parse.Validate(p); err != nil {
		return nil, err
	}
	return &Pattern{r: []rune(p)}, nil
}

// Match reports whether p matches s as a whole. Invalid patterns or
// over-long text yield false; use Compile/parse.ValidateText for the error.
func Match(s, p string) bool {
	q, err := Compile(p)
	if err != nil || parse.ValidateText(s) != nil {
		return false
	}
	return q.Match(s)
}

// Match reports whether the compiled pattern matches s as a whole.
func (p *Pattern) Match(s string) bool {
	e := engine{pat: p.r, txt: []rune(s), starP: -1}
	return e.run()
}

// engine holds one Match call's two pointers and backtracking state. steps
// counts forward pointer advances; it is unexported and never returned by any
// exported API (internal tests read it, LinearAdvance returns only a verdict).
type engine struct {
	pat, txt []rune
	i, j     int // text pointer / pattern pointer
	starP    int // pattern index of the most recently seen '*'
	matchS   int // text index that '*' currently claims
	steps    int
}

// run is greedy two-pointer matching: at '*' consume empty and advance only
// the pattern pointer; on a later mismatch, backtrack, feed that '*' one more
// rune and retry. Trailing '*' runes swallow all remaining text.
func (e *engine) run() bool {
	m, n := len(e.txt), len(e.pat)
	for e.i < m {
		switch {
		case e.j < n && (e.pat[e.j] == '?' || e.pat[e.j] == e.txt[e.i]):
			e.i++
			e.j++
			e.steps += 2
		case e.j < n && e.pat[e.j] == '*':
			e.starP = e.j
			e.matchS = e.i
			e.j++
			e.steps++
		case e.starP >= 0:
			e.j = e.starP + 1
			e.matchS++
			e.i = e.matchS
			e.steps++ // text pointer advances by one net rune
		default:
			return false
		}
	}
	for e.j < n && e.pat[e.j] == '*' {
		e.j++
		e.steps++
	}
	return e.j == n
}

// linearFactor bounds advances by linearFactor*(n+m); at fixed n the rescan
// coefficient is constant, so this is a constant multiple of 2*(n+m).
const linearFactor = 8

// LinearAdvance reports whether pointer advances stay within a linear budget
// of n+m while m spans two orders of magnitude. Verdict only.
func LinearAdvance(pattern string, sizes []int) bool {
	q, err := Compile(pattern)
	if err != nil {
		return false
	}
	texts := func(m int) []string {
		if m < 3 {
			return []string{strings.Repeat("a", m)}
		}
		return []string{
			strings.Repeat("a", m),
			strings.Repeat("a", m-1) + "b",
			strings.Repeat("a", m-2) + "cb",
		}
	}
	for _, m := range sizes {
		for _, s := range texts(m) {
			e := engine{pat: q.r, txt: []rune(s), starP: -1}
			_ = e.run()
			if e.steps > linearFactor*(len(q.r)+m) {
				return false
			}
		}
	}
	return true
}

// AgreesWithNaive reports whether greedy Match and naiveDP agree on one
// pair. It exposes only a verdict, so the reference implementation and the
// step counter never become part of the public surface.
func AgreesWithNaive(s, p string) bool {
	q, err := Compile(p)
	if err != nil || parse.ValidateText(s) != nil {
		return false
	}
	return q.Match(s) == naiveDP(s, p)
}

// naiveDP is the specification reference: dp[i][j] over rune prefixes,
// '*' = dp[i-1][j] || dp[i][j-1], '?'/literal = dp[i-1][j-1].
func naiveDP(s, p string) bool {
	sr, pr := []rune(s), []rune(p)
	m, n := len(sr), len(pr)
	prev := make([]bool, n+1)
	prev[0] = true
	for j := 1; j <= n; j++ {
		prev[j] = prev[j-1] && pr[j-1] == '*'
	}
	for i := 1; i <= m; i++ {
		cur := make([]bool, n+1)
		for j := 1; j <= n; j++ {
			switch pr[j-1] {
			case '*':
				cur[j] = prev[j] || cur[j-1]
			case '?', sr[i-1]:
				cur[j] = prev[j-1]
			}
		}
		prev = cur
	}
	return prev[n]
}

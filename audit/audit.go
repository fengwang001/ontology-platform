// Package audit provides a naive recursive reference matcher and differential
// cross-checking against the production linear matcher. The reference is
// intentionally exponential and exempt from step budgets; it exists only for
// tests and audits.
package audit

import (
	"math/rand/v2"

	"ontology/budget"
	"ontology/match"
	"ontology/pattern"
)

// NaiveMatch evaluates the pattern with plain backtracking recursion.
func NaiveMatch(p *pattern.Pattern, text []rune) bool {
	tokens := p.Tokens()
	var rec func(pi, ti int) bool
	rec = func(pi, ti int) bool {
		if pi == len(tokens) {
			return ti == len(text)
		}
		switch tok := tokens[pi]; tok.Kind {
		case pattern.AnySeq:
			// '%' consumes 0..len(text)-ti codepoints: every possible split.
			for k := ti; k <= len(text); k++ {
				if rec(pi+1, k) {
					return true
				}
			}
			return false
		case pattern.AnyChar:
			return ti < len(text) && rec(pi+1, ti+1)
		default:
			return ti < len(text) && text[ti] == tok.R && rec(pi+1, ti+1)
		}
	}
	return rec(0, 0)
}

// Case is one generated differential pair.
type Case struct {
	Pattern string
	Text    string
}

var patternAlphabet = []rune{'a', 'b', '%', '_'}
var textAlphabet = []rune{'a', 'b', '%', '_'}

// GenerateCases produces n deterministic random pairs from rng. Pattern
// length is 0..maxPat, text length 0..maxText, drawn from a,b,%,_.
func GenerateCases(rng *rand.Rand, n, maxPat, maxText int) []Case {
	cases := make([]Case, n)
	for i := range cases {
		cases[i] = Case{
			Pattern: randomString(rng, rng.IntN(maxPat+1), patternAlphabet),
			Text:    randomString(rng, rng.IntN(maxText+1), textAlphabet),
		}
	}
	return cases
}

func randomString(rng *rand.Rand, n int, alpha []rune) string {
	runes := make([]rune, n)
	for i := range runes {
		runes[i] = alpha[rng.IntN(len(alpha))]
	}
	return string(runes)
}

// Mismatch is a pair on which the two matchers disagree.
type Mismatch struct {
	C        Case
	Linear   bool
	Naive    bool
	LinearOK bool
}

// CrossCheck runs the production matcher and NaiveMatch over every case and
// returns all disagreements. With unlimited per-match steps, linearOK is true.
func CrossCheck(cases []Case) []Mismatch {
	var mismatches []Mismatch
	for _, c := range cases {
		p, perr := pattern.Parse(c.Pattern)
		if perr != nil {
			continue
		}
		linear, lerr := match.MatchParsed(p, c.Text, budget.New(0))
		naive := NaiveMatch(p, []rune(c.Text))
		if lerr != nil || linear != naive {
			mismatches = append(mismatches, Mismatch{
				C: c, Linear: linear, Naive: naive, LinearOK: lerr == nil,
			})
		}
	}
	return mismatches
}

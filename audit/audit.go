// Package audit holds a naive recursive reference matcher and a
// fixed-seed pair generator, used only in tests and differential
// checks. The reference has no step budget.
package audit

import (
	"math/rand/v2"

	"ontology/match"
	"ontology/pattern"
)

// Naive is the exponential recursive reference matcher.
func Naive(toks []pattern.Token, text []rune) bool {
	if len(toks) == 0 {
		return len(text) == 0
	}
	t := toks[0]
	switch t.Kind {
	case pattern.AnyMany:
		for k := 0; k <= len(text); k++ {
			if Naive(toks[1:], text[k:]) {
				return true
			}
		}
		return false
	case pattern.AnyOne:
		return len(text) > 0 && Naive(toks[1:], text[1:])
	default:
		return len(text) > 0 && text[0] == t.Lit && Naive(toks[1:], text[1:])
	}
}

// Agree reports whether the production matcher and the naive
// reference reach the same verdict for (pat, text).
func Agree(pat, text string) (agree bool, err error) {
	p, err := pattern.Parse(pat)
	if err != nil {
		return false, err
	}
	if err := pattern.CheckUTF8("text", text); err != nil {
		return false, err
	}
	prod, err := match.Match(p, text, nil)
	if err != nil {
		return false, err
	}
	ref := Naive(p.Tokens(), []rune(text))
	return prod == ref, nil
}

// Pairs deterministically generates n random (pattern, text) pairs.
// Patterns use the alphabet {a,b,%,_} with length 0..8, texts use
// {a,b,%,_} with length 0..10.
func Pairs(seed uint64, n int) [][2]string {
	rng := rand.New(rand.NewPCG(seed, 0x9e3779b97f4a7c15))
	patAlpha := []byte("ab%_")
	textAlpha := []byte("ab%_")
	pairs := make([][2]string, 0, n)
	for i := 0; i < n; i++ {
		pat := make([]byte, rng.IntN(9))
		for j := range pat {
			pat[j] = patAlpha[rng.IntN(len(patAlpha))]
		}
		text := make([]byte, rng.IntN(11))
		for j := range text {
			text[j] = textAlpha[rng.IntN(len(textAlpha))]
		}
		pairs = append(pairs, [2]string{string(pat), string(text)})
	}
	return pairs
}

package audit

import (
	"math/rand/v2"
	"testing"

	"ontology/budget"
	"ontology/match"
	"ontology/pattern"
)

const fixedSeed = uint64(0xC0FFEE)

func TestRandomCrossCheck(t *testing.T) {
	// Deterministic generation: same seed reproduces the same pairs.
	first := GenerateCases(rand.New(rand.NewPCG(fixedSeed, fixedSeed^0x9E3779B9)), 2000, 8, 10)
	second := GenerateCases(rand.New(rand.NewPCG(fixedSeed, fixedSeed^0x9E3779B9)), 2000, 8, 10)
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("nondeterministic generation at %d: %+v vs %+v", i, first[i], second[i])
		}
	}
	if len(first) < 2000 {
		t.Fatalf("generated %d cases, want >= 2000", len(first))
	}
	mismatches := CrossCheck(first)
	for _, m := range mismatches {
		t.Errorf("mismatch %+v: linear=%v naive=%v", m.C, m.Linear, m.Naive)
	}
}

func TestUnicodeCrossCheck(t *testing.T) {
	patterns := []string{"_", "__", "%_", "_%", "caf_", "a_b", "%😀%", `\%`, `\_`}
	texts := []string{"", "a", "ab", "cafe", "café", "😀", "a😀b", "😀😀", "%", "_", `\`, "aaa"}
	for _, pat := range patterns {
		p, err := pattern.Parse(pat)
		if err != nil {
			t.Fatalf("parse %q: %v", pat, err)
		}
		for _, text := range texts {
			linear, err := match.MatchParsed(p, text, budget.New(0))
			if err != nil {
				t.Fatalf("match %q %q: %v", pat, text, err)
			}
			naive := NaiveMatch(p, []rune(text))
			if linear != naive {
				t.Errorf("pat=%q text=%q linear=%v naive=%v", pat, text, linear, naive)
			}
		}
	}
}

package ontology

import (
	"math/rand"
	"strings"
	"testing"
)

// Two 1000-rune, completely different strings with k=2: the matcher must
// say "exceeded" and the filled-cell count must stay in O(k*len) — the
// hard upper bound is (2k+1)*(minLen+1) = 5*1001 = 5005 cells, and early
// termination drives it far lower — never anywhere near 1000*1000 = 10^6.
func TestThousandRuneCompletelyDifferent(t *testing.T) {
	a := strings.Repeat("a", 1000)
	b := strings.Repeat("b", 1000)
	res := mustDistance(t, a, b, 2)
	if !res.Exceeded {
		t.Fatalf("got %+v, want exceeded", res)
	}
	const bandBound = int64(5 * 1001) // (2k+1)*(minLen+1)
	if res.Stats.CellsFilled <= 0 || res.Stats.CellsFilled > bandBound {
		t.Fatalf("filled %d cells, want 0 < cells <= %d (O(k*len))", res.Stats.CellsFilled, bandBound)
	}
	if res.Stats.CellsFilled >= 1000*1000 {
		t.Fatalf("filled %d cells, must be far below the full 10^6 matrix", res.Stats.CellsFilled)
	}
}

// Distance(a,b) must equal Distance(b,a), including the filled-cell
// count: the band |i-j| <= k is symmetric under transposition and both
// directions terminate on the same (transposed) row.
func TestSymmetryIncludingCellCounts(t *testing.T) {
	pairs := [][2]string{
		{"kitten", "sitting"},
		{"", "abc"},
		{"café", "cafe"},
		{"a\U0001F600b", "ab"},
		{strings.Repeat("x", 500), strings.Repeat("y", 480)},
		{strings.Repeat("ab", 300), strings.Repeat("ba", 300)},
	}
	ks := []int{0, 1, 2, 3, 5, 25}
	for _, p := range pairs {
		for _, k := range ks {
			fwd := mustDistance(t, p[0], p[1], k)
			rev := mustDistance(t, p[1], p[0], k)
			if fwd.Exceeded != rev.Exceeded {
				t.Fatalf("(%q,%q,k=%d): exceeded %v vs %v", p[0], p[1], k, fwd.Exceeded, rev.Exceeded)
			}
			if !fwd.Exceeded && fwd.Distance != rev.Distance {
				t.Fatalf("(%q,%q,k=%d): distance %d vs %d", p[0], p[1], k, fwd.Distance, rev.Distance)
			}
			if fwd.Stats.CellsFilled != rev.Stats.CellsFilled {
				t.Fatalf("(%q,%q,k=%d): cells %d vs %d", p[0], p[1], k,
					fwd.Stats.CellsFilled, rev.Stats.CellsFilled)
			}
		}
	}
}

// Randomized cross-check: whenever Distance stays within k, its exact
// distance must equal the unlimited full-matrix distance; whenever it
// reports exceeded, the true distance must indeed be greater than k.
func TestRandomMatchesUnlimited(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	alphabet := []rune("abcé\U0001F600")
	for trial := 0; trial < 300; trial++ {
		a := randomRunes(rng, alphabet, 30)
		b := randomRunes(rng, alphabet, 30)
		k := rng.Intn(8)
		res := mustDistance(t, a, b, k)
		full, err := DistanceUnlimited(a, b)
		if err != nil {
			t.Fatalf("DistanceUnlimited: %v", err)
		}
		if res.Exceeded {
			if full <= k {
				t.Fatalf("(%q,%q,k=%d): reported exceeded but true distance %d", a, b, k, full)
			}
		} else if res.Distance != full {
			t.Fatalf("(%q,%q,k=%d): got %d, unlimited %d", a, b, k, res.Distance, full)
		}
	}
}

// The triangle inequality must hold for the exact distances returned
// within threshold: d(a,c) <= d(a,b) + d(b,c).
func TestTriangleInequality(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	alphabet := []rune("abcd")
	for trial := 0; trial < 200; trial++ {
		a := randomRunes(rng, alphabet, 12)
		b := randomRunes(rng, alphabet, 12)
		c := randomRunes(rng, alphabet, 12)
		dab, _ := DistanceUnlimited(a, b)
		dbc, _ := DistanceUnlimited(b, c)
		dac, _ := DistanceUnlimited(a, c)
		if dac > dab+dbc {
			t.Fatalf("triangle violated: d(a,c)=%d > d(a,b)=%d + d(b,c)=%d", dac, dab, dbc)
		}
		// And the thresholded matcher must agree whenever k covers it.
		res := mustDistance(t, a, c, dab+dbc)
		if res.Exceeded || res.Distance != dac {
			t.Fatalf("thresholded d(a,c) = %+v, want %d", res, dac)
		}
	}
}

func randomRunes(rng *rand.Rand, alphabet []rune, maxLen int) string {
	n := rng.Intn(maxLen + 1)
	var sb strings.Builder
	for i := 0; i < n; i++ {
		sb.WriteRune(alphabet[rng.Intn(len(alphabet))])
	}
	return sb.String()
}

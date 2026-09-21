package ontology

import (
	"strings"
	"testing"
)

// Within the threshold, Compare must return the exact distance, identical
// to the unbounded full DP.
func TestExactDistanceMatchesFull(t *testing.T) {
	pairs := [][2]string{
		{"kitten", "sitting"},
		{"flaw", "lawn"},
		{"gumbo", "gambol"},
		{"", ""},
		{"abc", "abc"},
		{"book", "back"},
		{"café", "cafe"},
		{"a😀b", "ab"},
		{" Saturday", "Sunday"},
	}
	for _, fold := range []bool{false, true} {
		m := Matcher{FoldCase: fold}
		for _, p := range pairs {
			full, err := FullDistance(p[0], p[1], fold)
			if err != nil {
				t.Fatal(err)
			}
			res, err := m.Compare(p[0], p[1], full)
			if err != nil {
				t.Fatal(err)
			}
			if res.Exceeded {
				t.Fatalf("Compare(%q,%q,k=%d) exceeded, want distance %d", p[0], p[1], full, full)
			}
			if res.Distance != full {
				t.Fatalf("Compare(%q,%q,k=%d)=%d, want %d", p[0], p[1], full, res.Distance, full)
			}
			// One below the true distance must report Exceeded.
			if full > 0 {
				under, err := m.Compare(p[0], p[1], full-1)
				if err != nil {
					t.Fatal(err)
				}
				if !under.Exceeded {
					t.Fatalf("Compare(%q,%q,k=%d) not exceeded, want exceeded (distance %d)", p[0], p[1], full-1, full)
				}
			}
		}
	}
}

// Two totally different 1000-rune strings with k=2 must terminate early:
// cells filled are O(k*len), nowhere near len*len = 1e6.
func TestEarlyTerminationCellCount(t *testing.T) {
	const n = 1000
	const k = 2
	a := strings.Repeat("a", n)
	b := strings.Repeat("b", n)
	m := Matcher{}
	res, err := m.Compare(a, b, k)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Exceeded {
		t.Fatal("expected Exceeded for completely different strings")
	}
	// Hard upper bound: at most (2k+1) band cells per row times n rows.
	bandBound := (2*k + 1) * n
	if res.Stats.Cells > bandBound {
		t.Fatalf("cells %d exceed band bound %d", res.Stats.Cells, bandBound)
	}
	// With early row abort the real count should be far smaller still,
	// and in any case orders of magnitude below n*n.
	if res.Stats.Cells >= n*n/100 {
		t.Fatalf("cells %d not O(k*len); n*n=%d", res.Stats.Cells, n*n)
	}
	t.Logf("cells filled: %d (band bound %d, full grid %d)", res.Stats.Cells, bandBound, n*n)
}

// Distance and cell count must be symmetric, including for inputs of
// different lengths.
func TestSymmetry(t *testing.T) {
	pairs := [][2]string{
		{"kitten", "sitting"},
		{"abcd", "ab"},
		{"", "xyz"},
		{"café", "cafe"},
		{"a😀b", "ab"},
		{"Straße", "strasse"},
	}
	for _, fold := range []bool{false, true} {
		m := Matcher{FoldCase: fold}
		for _, p := range pairs {
			for _, k := range []int{0, 1, 3, 10} {
				r1, err := m.Compare(p[0], p[1], k)
				if err != nil {
					t.Fatal(err)
				}
				r2, err := m.Compare(p[1], p[0], k)
				if err != nil {
					t.Fatal(err)
				}
				if r1.Exceeded != r2.Exceeded || r1.Distance != r2.Distance {
					t.Fatalf("asymmetric result for %q vs %q k=%d: %+v vs %+v", p[0], p[1], k, r1, r2)
				}
				if r1.Stats.Cells != r2.Stats.Cells {
					t.Fatalf("asymmetric cell count for %q vs %q k=%d: %d vs %d",
						p[0], p[1], k, r1.Stats.Cells, r2.Stats.Cells)
				}
			}
		}
	}
}

// Simple case folding equalizes ASCII case and the Kelvin sign, but (as
// documented) not full-fold cases like 'ß' vs "ss".
func TestFoldCase(t *testing.T) {
	plain := Matcher{}
	fold := Matcher{FoldCase: true}

	res, err := fold.Compare("Hello", "hELLO", 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.Exceeded || res.Distance != 0 {
		t.Fatalf("folded Hello/hELLO: got %+v, want distance 0", res)
	}

	res, err = plain.Compare("Hello", "hELLO", 10)
	if err != nil {
		t.Fatal(err)
	}
	if res.Exceeded || res.Distance == 0 {
		t.Fatalf("unfolded Hello/hELLO: got %+v, want nonzero distance", res)
	}

	// U+212A KELVIN SIGN simple-folds to 'k'.
	res, err = fold.Compare("K", "k", 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.Exceeded || res.Distance != 0 {
		t.Fatalf("folded Kelvin/k: got %+v, want distance 0", res)
	}

	// Full folding is out of scope: 'ß' stays a single rune.
	res, err = fold.Compare("Straße", "STRASSE", 10)
	if err != nil {
		t.Fatal(err)
	}
	if res.Exceeded || res.Distance == 0 {
		t.Fatalf("folded Straße/STRASSE: got %+v, want nonzero distance", res)
	}
}

// Triangle inequality sanity check via exact distances.
func TestTriangleInequality(t *testing.T) {
	words := []string{"", "a", "ab", "abc", "acb", "b", "café", "cafe"}
	for _, x := range words {
		for _, y := range words {
			for _, z := range words {
				dxy, _ := FullDistance(x, y, false)
				dyz, _ := FullDistance(y, z, false)
				dxz, _ := FullDistance(x, z, false)
				if dxz > dxy+dyz {
					t.Fatalf("triangle violated: d(%q,%q)=%d > %d+%d", x, z, dxz, dxy, dyz)
				}
			}
		}
	}
}

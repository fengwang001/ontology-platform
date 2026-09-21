package ontology

import (
	"errors"
	"testing"
)

func mustDistance(t *testing.T, a, b string, k int) Result {
	t.Helper()
	res, err := Distance(a, b, k)
	if err != nil {
		t.Fatalf("Distance(%q, %q, %d) unexpected error: %v", a, b, k, err)
	}
	return res
}

func TestExactDistanceMatchesUnlimited(t *testing.T) {
	cases := []struct {
		a, b string
		k    int
	}{
		{"kitten", "sitting", 3},
		{"kitten", "sitting", 7},
		{"flaw", "lawn", 2},
		{"abc", "abc", 0},
		{"abc", "abd", 1},
		{"", "abc", 3},
		{"abcdef", "azced", 3},
		{"a", "b", 1},
	}
	for _, tc := range cases {
		res := mustDistance(t, tc.a, tc.b, tc.k)
		if res.Exceeded {
			t.Fatalf("Distance(%q, %q, %d) reported exceeded, want exact", tc.a, tc.b, tc.k)
		}
		full, err := DistanceUnlimited(tc.a, tc.b)
		if err != nil {
			t.Fatalf("DistanceUnlimited(%q, %q): %v", tc.a, tc.b, err)
		}
		if res.Distance != full {
			t.Fatalf("Distance(%q, %q, %d) = %d, unlimited = %d", tc.a, tc.b, tc.k, res.Distance, full)
		}
	}
}

func TestNegativeK(t *testing.T) {
	_, err := Distance("a", "a", -1)
	if !errors.Is(err, ErrNegativeK) {
		t.Fatalf("Distance with k=-1: got %v, want ErrNegativeK", err)
	}
}

func TestZeroKIsEquality(t *testing.T) {
	res := mustDistance(t, "same", "same", 0)
	if res.Exceeded || res.Distance != 0 {
		t.Fatalf("equal strings with k=0: got %+v, want distance 0", res)
	}
	res = mustDistance(t, "same", "diff", 0)
	if !res.Exceeded {
		t.Fatalf("different strings with k=0: got %+v, want exceeded", res)
	}
	res = mustDistance(t, "a", "aa", 0)
	if !res.Exceeded {
		t.Fatalf("different lengths with k=0: got %+v, want exceeded", res)
	}
}

func TestEmptyStrings(t *testing.T) {
	res := mustDistance(t, "", "", 0)
	if res.Exceeded || res.Distance != 0 {
		t.Fatalf("both empty: got %+v, want distance 0", res)
	}
	res = mustDistance(t, "", "abc", 3)
	if res.Exceeded || res.Distance != 3 {
		t.Fatalf("empty vs abc k=3: got %+v, want distance 3", res)
	}
	res = mustDistance(t, "abc", "", 2)
	if !res.Exceeded {
		t.Fatalf("abc vs empty k=2: got %+v, want exceeded", res)
	}
}

func TestLengthDiffExceedsKFillsZeroCells(t *testing.T) {
	res := mustDistance(t, "aaaaaa", "aa", 2)
	if !res.Exceeded {
		t.Fatalf("length diff 4 with k=2: got %+v, want exceeded", res)
	}
	if res.Stats.CellsFilled != 0 {
		t.Fatalf("length diff > k: filled %d cells, want 0", res.Stats.CellsFilled)
	}
}

func TestEarlyTerminatesWhenExceeded(t *testing.T) {
	a := repeatString("a", 1000)
	b := repeatString("b", 1000)
	res := mustDistance(t, a, b, 2)
	if !res.Exceeded {
		t.Fatalf("1000 a's vs 1000 b's with k=2: got %+v, want exceeded", res)
	}
	// Early termination must fire within the first few rows: the filled
	// cell count is a tiny fraction of the band upper bound
	// (2k+1)*(n+1) = 5*1001 = 5005, and of the full 10^6 matrix.
	if res.Stats.CellsFilled <= 0 || res.Stats.CellsFilled > 100 {
		t.Fatalf("early termination: filled %d cells, want a small positive count", res.Stats.CellsFilled)
	}
}

func repeatString(s string, n int) string {
	out := ""
	for len(out) < n*len(s) {
		out += s
	}
	return out
}

package ontology

import (
	"strings"
	"testing"
)

// Two 1000-rune strings at distance exactly 2, compared with k=2: the
// band runs to completion, so the filled-cell count is exactly
//
//	(2k+1)*(n+1) - k*(k+1) = 5*1001 - 6 = 4999
//
// where k*(k+1) accounts for the triangular corners the band misses at
// the top-left and bottom-right. This is the O(k*len) upper bound in
// action: 4999 cells instead of 1001*1001 = 1002001 for a full matrix.
func TestBandCellCountIsOrderKTimesLen(t *testing.T) {
	a := []rune(repeatString("a", 1000))
	b := make([]rune, len(a))
	copy(b, a)
	b[123] = 'x'
	b[877] = 'y'
	res := mustDistance(t, string(a), string(b), 2)
	if res.Exceeded || res.Distance != 2 {
		t.Fatalf("got %+v, want exact distance 2", res)
	}
	const want = 5*1001 - 6
	if res.Stats.CellsFilled != want {
		t.Fatalf("filled %d cells, want exactly %d (O(k*len) band)", res.Stats.CellsFilled, want)
	}
	if res.Stats.MaxWorkArrayLen != 5 {
		t.Fatalf("work array len %d, want 2k+1 = 5", res.Stats.MaxWorkArrayLen)
	}
}

// Two 1000-rune strings that are completely different, k=2: the distance
// is 1000, far above k, so early termination fires after a handful of
// rows. The cell count stays far below even one band sweep.
func TestCompletelyDifferentStringsTerminateEarly(t *testing.T) {
	a := repeatString("a", 1000)
	b := repeatString("b", 1000)
	res := mustDistance(t, a, b, 2)
	if !res.Exceeded {
		t.Fatalf("got %+v, want exceeded", res)
	}
	const bandSweep = (2*2 + 1) * 1001 // 5005, the no-termination bound
	if res.Stats.CellsFilled <= 0 || res.Stats.CellsFilled >= bandSweep {
		t.Fatalf("filled %d cells, want 0 < cells < %d", res.Stats.CellsFilled, bandSweep)
	}
	if res.Stats.CellsFilled >= 1000*1000 {
		t.Fatalf("filled %d cells, must be far below the full 10^6 matrix", res.Stats.CellsFilled)
	}
}

// 100k runes per side, k=3: the rolling work arrays have length 2k+1 = 7
// regardless of input size, and the filled-cell count is O(k*len).
func TestWorkArrayLengthIsIndependentOfInputSize(t *testing.T) {
	const n = 100_000
	a := []rune(strings.Repeat("ab", n/2))
	b := make([]rune, len(a))
	copy(b, a)
	b[10] = 'x'
	b[n/2] = 'y'
	b[n-10] = 'z'
	res := mustDistance(t, string(a), string(b), 3)
	if res.Exceeded || res.Distance != 3 {
		t.Fatalf("got %+v, want exact distance 3", res)
	}
	if res.Stats.MaxWorkArrayLen != 7 {
		t.Fatalf("work array len %d, want 2k+1 = 7", res.Stats.MaxWorkArrayLen)
	}
	if res.Stats.MaxWorkArrayLen >= n/10 {
		t.Fatalf("work array len %d must be far below input size %d", res.Stats.MaxWorkArrayLen, n)
	}
	if res.Stats.CellsFilled >= int64(n)*int64(n) {
		t.Fatalf("filled %d cells, must be far below n^2", res.Stats.CellsFilled)
	}
}

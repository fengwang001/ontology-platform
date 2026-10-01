package cursor

import (
	"math"
	"strings"
	"testing"
)

func TestTargetedEdges(t *testing.T) {
	r := NewRegistry()
	var log strings.Builder

	mustOpen := func(name string, n int64, scroll bool) {
		t.Helper()
		if err := r.Open(name, n, scroll); err != nil {
			t.Fatalf("open %s: %v", name, err)
		}
	}

	// ABSOLUTE -1 is the last row; ABSOLUTE 0 moves to the before edge.
	mustOpen("abs", 5, true)
	m := newOracle(5, true)
	mustFetch(t, r, "abs", m, OpAbsolute, -1, &log)
	mustFetch(t, r, "abs", m, OpAbsolute, 0, &log)
	mustFetch(t, r, "abs", m, OpAbsolute, -6, &log) // lands at position 0
	mustFetch(t, r, "abs", m, OpAbsolute, 6, &log)  // lands at position n+1

	// PRIOR from n+1 returns row n; NEXT past the end then PRIOR returns n.
	mustOpen("edges", 5, true)
	me := newOracle(5, true)
	mustFetch(t, r, "edges", me, OpAbsolute, 100, &log) // p = n+1
	mustFetch(t, r, "edges", me, OpPrior, 0, &log)      // row n
	mustFetch(t, r, "edges", me, OpNext, 0, &log)       // p = n+1, no row
	mustFetch(t, r, "edges", me, OpNext, 0, &log)       // stays at n+1
	mustFetch(t, r, "edges", me, OpPrior, 0, &log)      // row n again

	// RELATIVE 0 repeats the current row inside the set and returns nothing
	// on either edge.
	mustOpen("rel", 5, true)
	mr := newOracle(5, true)
	mustFetch(t, r, "rel", mr, OpRelative, 0, &log) // p=0, no row
	mustFetch(t, r, "rel", mr, OpNext, 0, &log)     // row 1
	mustFetch(t, r, "rel", mr, OpRelative, 0, &log) // row 1 again
	mustFetch(t, r, "rel", mr, OpAbsolute, 6, &log) // p = n+1
	mustFetch(t, r, "rel", mr, OpRelative, 0, &log) // no row, stays n+1

	// Empty result set: every legal fetch lands on an edge.
	mustOpen("empty", 0, true)
	mz := newOracle(0, true)
	mustFetch(t, r, "empty", mz, OpFirst, 0, &log)
	mustFetch(t, r, "empty", mz, OpLast, 0, &log)
	mustFetch(t, r, "empty", mz, OpNext, 0, &log)
	mustFetch(t, r, "empty", mz, OpPrior, 0, &log)
	mustFetch(t, r, "empty", mz, OpAbsolute, -1, &log)
	mustFetch(t, r, "empty", mz, OpForward, 3, &log)
	mustFetch(t, r, "empty", mz, OpBackward, 3, &log)

	// FORWARD exactly exhausting the set ends on the last row; a short scan
	// ends at n+1.
	mustOpen("fwd", 5, true)
	mf := newOracle(5, true)
	mustFetch(t, r, "fwd", mf, OpForward, 5, &log) // rows 1..5, p=5
	mustFetch(t, r, "fwd", mf, OpForward, 3, &log) // no rows left, p=6

	mustOpen("fwd2", 5, true)
	mf2 := newOracle(5, true)
	mustFetch(t, r, "fwd2", mf2, OpForward, 3, &log) // rows 1..3, p=3
	mustFetch(t, r, "fwd2", mf2, OpForward, 3, &log) // rows 4,5 short, p=6

	// BACKWARD starting from the after-last edge scans from row n downward.
	mustOpen("bwd", 5, true)
	mb := newOracle(5, true)
	mustFetch(t, r, "bwd", mb, OpForward, 10, &log)  // p=6
	mustFetch(t, r, "bwd", mb, OpBackward, 3, &log)  // rows 5,4,3
	mustFetch(t, r, "bwd", mb, OpBackward, 10, &log) // rows 2,1 short, p=0

	// Forward-only cursor rejects backward-style movement.
	mustOpen("fo", 5, false)
	if _, err := r.Fetch("fo", OpPrior, 0); err != errForwardOnly {
		t.Fatalf("PRIOR on forward-only: got %v, want %v", err, errForwardOnly)
	}
	if _, err := r.Fetch("fo", OpBackward, 1); err != errForwardOnly {
		t.Fatalf("BACKWARD on forward-only must report forward_only first: got %v", err)
	}
	if _, err := r.Fetch("fo", OpRelative, -1); err != errForwardOnly {
		t.Fatalf("negative RELATIVE on forward-only: got %v", err)
	}
	if _, err := r.Fetch("fo", OpForward, 0); err != errNonPositiveCount {
		t.Fatalf("FORWARD 0: got %v, want %v", err, errNonPositiveCount)
	}
	if pos, _ := r.Position("fo"); pos != 0 {
		t.Fatalf("rejected operations moved position to %d", pos)
	}

	t.Log("targeted edge trace:\n" + log.String())
}

func TestRejectionOrder(t *testing.T) {
	r := NewRegistry()
	if err := r.Open("", 1, true); err != errEmptyName {
		t.Fatalf("empty name: got %v", err)
	}
	if err := r.Open("a", -1, true); err != errNegativeRows {
		t.Fatalf("negative n: got %v", err)
	}
	if err := r.Open("a", 1<<40+1, true); err != errRowsTooLarge {
		t.Fatalf("oversized n: got %v", err)
	}
	if err := r.Open("", -1, true); err != errEmptyName {
		t.Fatalf("order: empty name must win over negative n, got %v", err)
	}
	if err := r.Open("dup", 3, true); err != nil {
		t.Fatal(err)
	}
	if err := r.Open("dup", 3, true); err != errNameExists {
		t.Fatalf("duplicate open: got %v", err)
	}

	if _, err := r.Fetch("missing", OpNext, 0); err != errNotFound {
		t.Fatalf("fetch missing: got %v", err)
	}
	if err := r.Close("dup"); err != nil {
		t.Fatal(err)
	}
	if err := r.Open("dup", 1, true); err != nil {
		t.Fatalf("reopen after close: %v", err)
	}

	if _, err := r.Fetch("dup", Op("SIDEWAYS"), 0); err != errInvalidOp {
		t.Fatalf("bad op: got %v", err)
	}
	if err := r.Open("fonly", 3, false); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Fetch("fonly", OpBackward, 0); err != errForwardOnly {
		t.Fatalf("forward-only check precedes count check: got %v", err)
	}
	if _, err := r.Fetch("fonly", OpBackward, 1); err != errForwardOnly {
		t.Fatalf("BACKWARD on forward-only: got %v", err)
	}
	if _, err := r.Position("ghost"); err != errNotFound {
		t.Fatalf("position missing: got %v", err)
	}
	if err := r.Close("ghost"); err != errNotFound {
		t.Fatalf("close missing: got %v", err)
	}
}

func TestInt64Extremes(t *testing.T) {
	r := NewRegistry()
	var log strings.Builder
	// n=2^40 is the largest legal result set.
	const n = int64(1) << 40
	if err := r.Open("big", n, true); err != nil {
		t.Fatal(err)
	}
	m := newOracle(n, true)
	for _, c := range []struct {
		op Op
		k  int64
	}{
		{OpAbsolute, math.MaxInt64},
		{OpAbsolute, math.MinInt64},
		{OpRelative, math.MaxInt64},
		{OpRelative, math.MinInt64},
	} {
		mustFetch(t, r, "big", m, c.op, c.k, &log)
	}
	// Boundary row numbers on the largest set.
	mustFetch(t, r, "big", m, OpAbsolute, n, &log)
	mustFetch(t, r, "big", m, OpAbsolute, -n, &log) // n+1-n = 1
	mustFetch(t, r, "big", m, OpAbsolute, -1, &log) // row n

	if err := r.Open("toobig", n+1, true); err != errRowsTooLarge {
		t.Fatalf("n=2^40+1: got %v", err)
	}

	// Extremes on a forward-only cursor: negative RELATIVE is rejected
	// before any arithmetic; positive extremes simply run out of rows.
	if err := r.Open("bigfo", n, false); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Fetch("bigfo", OpRelative, math.MinInt64); err != errForwardOnly {
		t.Fatalf("MinInt64 RELATIVE on forward-only: got %v", err)
	}

	// Multi-row scans with extreme counts on a tiny set exhaust the set
	// without allocating proportional to k.
	if err := r.Open("tiny", 3, true); err != nil {
		t.Fatal(err)
	}
	mt := newOracle(3, true)
	mustFetch(t, r, "tiny", mt, OpForward, math.MaxInt64, &log)
	mustFetch(t, r, "tiny", mt, OpBackward, math.MaxInt64, &log)
	mustFetch(t, r, "tiny", mt, OpRelative, math.MinInt64, &log)
	mustFetch(t, r, "tiny", mt, OpAbsolute, math.MinInt64, &log)

	t.Log("int64 extreme trace:\n" + log.String())
}

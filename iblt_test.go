package ontology

import (
	"errors"
	"slices"
	"testing"
)

// cellsOf locks the sketch and copies its cells for white-box assertions.
func cellsOf(t *testing.T, s *IBLT) []cell {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]cell(nil), s.cells...)
}

func expectErrIs(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("expected error %v, got %v", target, err)
	}
}

func TestNewRejectsBadSize(t *testing.T) {
	for _, m := range []int{0, -3, 1, 2, 4, 5, 7, 10, 100} {
		if _, err := New(m); !errors.Is(err, ErrInvalidSize) {
			t.Fatalf("New(%d) err = %v, want ErrInvalidSize", m, err)
		}
	}
	for _, m := range []int{3, 6, 300} {
		if s, err := New(m); err != nil || s.M() != m {
			t.Fatalf("New(%d) = %v, %v", m, s, err)
		}
	}
}

func TestKeyZeroRecovered(t *testing.T) {
	a, _ := New(300)
	b, _ := New(300)
	a.Add(0)
	a.Add(42)
	b.Add(42)

	diff, err := Subtract(a, b)
	if err != nil {
		t.Fatal(err)
	}
	onlyA, onlyB, err := diff.Decode(10)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	t.Logf("input A={0,42} B={42}; output onlyA=%v onlyB=%v; basis=all cells peeled to zero",
		onlyA, onlyB)
	if !slices.Equal(onlyA, []uint64{0}) || len(onlyB) != 0 {
		t.Fatalf("got onlyA=%v onlyB=%v, want [0] / []", onlyA, onlyB)
	}

	// Decode must not mutate the sketch.
	againA, againB, err := diff.Decode(10)
	if err != nil || !slices.Equal(againA, onlyA) || !slices.Equal(againB, onlyB) {
		t.Fatalf("second decode changed: %v %v %v", againA, againB, err)
	}
}

// TestImpureCellCountOne brute-forces three keys mapping to a common cell and
// inserts +1,+1,-1. The shared cell has count 1 but holds three distinct
// keys, so its checksum cannot match and it must not be pure.
func TestImpureCellCountOne(t *testing.T) {
	const m = 300
	const seg = m / 3
	type triple struct{ x, y, z uint64 }

	find := func(wantPos int) (triple, bool) {
		var candidates []uint64
		for x := uint64(1); x <= 200000 && len(candidates) < 80; x++ {
			if positions(x, seg)[0] == wantPos {
				candidates = append(candidates, x)
			}
		}
		for i := 0; i+2 < len(candidates); i++ {
			for j := i + 1; j+1 < len(candidates); j++ {
				for k := j + 1; k < len(candidates); k++ {
					x, y, z := candidates[i], candidates[j], candidates[k]
					if x^y^z == 0 {
						continue
					}
					return triple{x, y, z}, true
				}
			}
		}
		return triple{}, false
	}

	var got triple
	found := false
	for pos := 0; pos < seg && !found; pos++ {
		if tr, ok := find(pos); ok {
			got, found = tr, true
		}
	}
	if !found {
		t.Fatal("brute force failed to construct three colliding keys")
	}
	t.Logf("brute-forced keys %d,%d,%d sharing one segment-0 cell; inserts +1,+1,-1",
		got.x, got.y, got.z)

	s, _ := New(m)
	s.Add(got.x)
	s.Add(got.y)
	s.Remove(got.z)

	shared := positions(got.x, seg)[0]
	c := cellsOf(t, s)[shared]
	if c.count != 1 {
		t.Fatalf("shared cell count = %d, want 1", c.count)
	}
	if g(c.key) == c.check {
		t.Fatalf("count-1 cell wrongly looks pure: %+v", c)
	}
	t.Logf("shared cell %+v: count=1 but g(keyXor)!=checkXor => not pure; "+
		"basis=purity requires count in +/-1 AND checksum match", c)
}

// TestCountZeroNonzeroComponents verifies a cell with count 0 and non-zero
// XOR accumulators is not treated as pure: the scan must skip it and the
// undecodable remainder is reported.
func TestCountZeroNonzeroComponents(t *testing.T) {
	// One cell holds two cancelling insertions (+1,-1): count 0, non-zero
	// XOR accumulators. Every other cell is zero, so no peel can start.
	s, _ := New(300)
	s.mu.Lock()
	s.cells[7] = cell{count: 0, key: 3, check: 9388199053230452220}
	s.mu.Unlock()

	c := cellsOf(t, s)[7]
	t.Logf("synthetic cell: %+v; basis=purity requires count in +/-1", c)

	_, _, err := s.Decode(100)
	expectErrIs(t, err, ErrUndecodable)
	t.Logf("judgment=%v: no pure cell exists and non-zero cells remain", err)
}

// TestMinusOneKeyGoesToBAndUndoesWithAdd inserts a key only on the b side:
// the peeled key must land in onlyB and undoing it applies Add's effect.
func TestMinusOneKeyGoesToBAndUndoesWithAdd(t *testing.T) {
	a, _ := New(300)
	b, _ := New(300)
	b.Add(77)
	diff, err := Subtract(a, b)
	if err != nil {
		t.Fatal(err)
	}

	onlyA, onlyB, err := diff.Decode(10)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	t.Logf("input A={} B={77}; output onlyA=%v onlyB=%v; basis=count -1 => onlyB, "+
		"undone via Add (+1,+xor,+xor)", onlyA, onlyB)
	if len(onlyA) != 0 || !slices.Equal(onlyB, []uint64{77}) {
		t.Fatalf("got %v / %v", onlyA, onlyB)
	}
	before := cellsOf(t, diff)
	if _, _, err := diff.Decode(10); err != nil {
		t.Fatal(err)
	}
	if cs := cellsOf(t, diff); !slices.Equal(cs, before) {
		t.Fatalf("Decode mutated the diff sketch:\nbefore=%+v\nafter =%+v", before, cs)
	}
}

func TestLimitBoundary(t *testing.T) {
	a, _ := New(300)
	b, _ := New(300)
	const n = 5
	for i := uint64(1); i <= n; i++ {
		a.Add(i * 1000)
	}
	diff, err := Subtract(a, b)
	if err != nil {
		t.Fatal(err)
	}

	onlyA, _, err := diff.Decode(n)
	if err != nil {
		t.Fatalf("limit == diff size should succeed, got %v", err)
	}
	t.Logf("limit=%d equal to diff size: success, onlyA=%v; basis=limit-th key "+
		"recorded and then every cell is zero", n, onlyA)
	if len(onlyA) != n {
		t.Fatalf("got %d keys, want %d", len(onlyA), n)
	}

	_, _, err = diff.Decode(n - 1)
	expectErrIs(t, err, ErrLimitExceeded)
	t.Logf("limit=%d one below diff size: judgment=%v; basis=about to record "+
		"the %d-th key", n-1, err, n)

	_, _, err = diff.Decode(-1)
	expectErrIs(t, err, ErrNegativeLimit)
	t.Logf("limit=-1: judgment=%v; basis=negative limit is checked first", err)
}

func TestUndecodableTinySketchNoMutation(t *testing.T) {
	// m=3 gives one cell per segment; all keys collide in every segment.
	a, _ := New(3)
	b, _ := New(3)
	for i := uint64(1); i <= 4; i++ {
		a.Add(i)
	}
	for i := uint64(3); i <= 6; i++ {
		b.Add(i)
	}

	beforeA := cellsOf(t, a)
	beforeB := cellsOf(t, b)

	diff, err := Subtract(a, b)
	if err != nil {
		t.Fatal(err)
	}
	beforeDiff := cellsOf(t, diff)
	t.Logf("input A={1..4} B={3..6} m=3; diff cells before decode=%+v", beforeDiff)

	_, _, err = diff.Decode(1000)
	expectErrIs(t, err, ErrUndecodable)
	t.Logf("judgment=%v; basis=peeling stops with no pure cell and non-zero cells remain", err)

	if got := cellsOf(t, diff); !slices.Equal(got, beforeDiff) {
		t.Fatalf("Decode mutated the diff sketch:\nbefore=%+v\nafter =%+v", beforeDiff, got)
	}
	if got := cellsOf(t, a); !slices.Equal(got, beforeA) {
		t.Fatal("operand a mutated")
	}
	if got := cellsOf(t, b); !slices.Equal(got, beforeB) {
		t.Fatal("operand b mutated")
	}
}

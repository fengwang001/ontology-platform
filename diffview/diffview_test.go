package diffview

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

// step applies one change, logs input/output/rationale, and asserts the
// expected outcome. wantErr is a sentinel (nil for success); wantEmit
// says whether a log entry is expected.
func step(t *testing.T, v *View, c Change, wantErr error, wantEmit bool, rationale string) Entry {
	t.Helper()
	entry, ok, err := v.Apply(c)
	if wantErr != nil {
		if !errors.Is(err, wantErr) {
			t.Fatalf("Apply(%+v): want error %v, got %v", c, wantErr, err)
		}
		t.Logf("input=%+v -> REJECTED (%v) | %s", c, err, rationale)
		return Entry{}
	}
	if err != nil {
		t.Fatalf("Apply(%+v): unexpected error %v", c, err)
	}
	if ok != wantEmit {
		t.Fatalf("Apply(%+v): emit=%v, want %v", c, ok, wantEmit)
	}
	if wantEmit {
		t.Logf("input=%+v -> emit %+v | %s", c, entry, rationale)
	} else {
		t.Logf("input=%+v -> no output | %s", c, rationale)
	}
	return entry
}

// checkConsistency verifies snapshot == batch recompute, every log
// prefix replays to a non-negative view, and the full replay equals the
// snapshot.
func checkConsistency(t *testing.T, v *View, left, right map[string]int) {
	t.Helper()
	snap := v.Snapshot()
	batch := Batch(left, right)
	if !reflect.DeepEqual(snap, batch) {
		t.Fatalf("snapshot %v != batch recompute %v", snap, batch)
	}
	log := v.Log()
	for n := 0; n <= len(log); n++ {
		prefix, err := Replay(log, n)
		if err != nil {
			t.Fatalf("prefix %d replay failed: %v", n, err)
		}
		for row, cnt := range prefix {
			if cnt <= 0 {
				t.Fatalf("prefix %d: row %q has non-positive multiplicity %d", n, row, cnt)
			}
		}
	}
	full, err := Replay(log, len(log))
	if err != nil {
		t.Fatalf("full replay failed: %v", err)
	}
	if !reflect.DeepEqual(full, snap) {
		t.Fatalf("full replay %v != snapshot %v", full, snap)
	}
	if err := v.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
	t.Logf("consistent: snapshot=%v batch=%v logLen=%d", snap, batch, len(log))
}

// apply mirrors an accepted change into the reference left/right counts.
func apply(t *testing.T, left, right map[string]int, c Change) {
	t.Helper()
	m := left
	if c.Side == Right {
		m = right
	}
	m[c.Row] += c.Delta
	if m[c.Row] < 0 {
		t.Fatalf("reference counts went negative for %+v", c)
	}
	if m[c.Row] == 0 {
		delete(m, c.Row)
	}
}

func TestRightSideArrivesFirst(t *testing.T) {
	v := New(0)
	left, right := map[string]int{}, map[string]int{}

	c := Change{Right, "a", 2}
	step(t, v, c, nil, false,
		"right first: result stays max(0-2,0)=0, no output")
	apply(t, left, right, c)
	checkConsistency(t, v, left, right)

	c = Change{Left, "a", 1}
	step(t, v, c, nil, false,
		"left=1 right=2: max(1-2,0)=0, unchanged, no output")
	apply(t, left, right, c)
	checkConsistency(t, v, left, right)

	c = Change{Left, "a", 1}
	step(t, v, c, nil, false,
		"left=2 right=2: max(2-2,0)=0, unchanged, no output")
	apply(t, left, right, c)
	checkConsistency(t, v, left, right)

	c = Change{Left, "a", 1}
	e := step(t, v, c, nil, true,
		"left=3 right=2: result 0->1, emit +1")
	if e.Before != 0 || e.After != 1 || e.ViewDelta() != 1 {
		t.Fatalf("unexpected entry %+v", e)
	}
	apply(t, left, right, c)
	checkConsistency(t, v, left, right)

	c = Change{Right, "a", -2}
	e = step(t, v, c, nil, true,
		"delete right 2: left=3 right=0, result 1->3, emit +2")
	if e.Before != 1 || e.After != 3 || e.ViewDelta() != 2 {
		t.Fatalf("unexpected entry %+v", e)
	}
	apply(t, left, right, c)
	checkConsistency(t, v, left, right)
}

func TestMinimalChange(t *testing.T) {
	v := New(0)
	left, right := map[string]int{}, map[string]int{}

	c := Change{Left, "x", 1}
	e := step(t, v, c, nil, true, "insert one copy: result 0->1, exactly one entry")
	if e.ViewDelta() != 1 || e.Seq != 1 {
		t.Fatalf("unexpected entry %+v", e)
	}
	apply(t, left, right, c)

	c = Change{Left, "x", -1}
	e = step(t, v, c, nil, true, "delete one copy: result 1->0, exactly one entry")
	if e.ViewDelta() != -1 || e.Seq != 2 {
		t.Fatalf("unexpected entry %+v", e)
	}
	apply(t, left, right, c)

	if got := v.Snapshot(); len(got) != 0 {
		t.Fatalf("zero-multiplicity row leaked into view: %v", got)
	}
	if len(v.Log()) != 2 {
		t.Fatalf("log length = %d, want 2", len(v.Log()))
	}
	checkConsistency(t, v, left, right)
}

func TestDeleteUnderflow(t *testing.T) {
	v := New(0)
	left, right := map[string]int{}, map[string]int{}

	c := Change{Left, "k", 1}
	step(t, v, c, nil, true, "seed one copy on the left")
	apply(t, left, right, c)

	snapBefore := v.Snapshot()
	logBefore := len(v.Log())

	step(t, v, Change{Left, "k", -2}, ErrDeleteUnderflow, false,
		"left has 1 copy, deleting 2 underflows -> reject")
	step(t, v, Change{Right, "k", -1}, ErrDeleteUnderflow, false,
		"right has 0 copies, deleting 1 underflows -> reject")
	step(t, v, Change{Left, "missing", -1}, ErrDeleteUnderflow, false,
		"deleting a row that never existed underflows -> reject")

	if got := v.Snapshot(); !reflect.DeepEqual(got, snapBefore) {
		t.Fatalf("state changed after rejections: %v != %v", got, snapBefore)
	}
	if len(v.Log()) != logBefore {
		t.Fatalf("log grew after rejections: %d != %d", len(v.Log()), logBefore)
	}
	checkConsistency(t, v, left, right)
}

func TestInvalidInputsRejectedAndStateUnchanged(t *testing.T) {
	v := New(2)
	left, right := map[string]int{}, map[string]int{}

	c := Change{Left, "r1", 1}
	step(t, v, c, nil, true, "seed row r1")
	apply(t, left, right, c)
	c = Change{Right, "r2", 1}
	step(t, v, c, nil, false, "seed row r2 on the right, no view output")
	apply(t, left, right, c)

	snapBefore := v.Snapshot()
	logBefore := v.Log()

	cases := []struct {
		c         Change
		wantErr   error
		rationale string
	}{
		{Change{Left, "", 1}, ErrEmptyRow, "empty row key is rejected"},
		{Change{Right, "", -1}, ErrEmptyRow, "empty row key is rejected on the right too"},
		{Change{Left, "r1", 0}, ErrZeroDelta, "zero delta is a no-op input and rejected"},
		{Change{Right, "r2", 0}, ErrZeroDelta, "zero delta rejected on the right"},
		{Change{Left, "r3", 1}, ErrRowLimit, "distinct rows already at limit 2, new row rejected"},
		{Change{Right, "r3", 1}, ErrRowLimit, "row limit applies to the right side as well"},
	}
	for _, tc := range cases {
		step(t, v, tc.c, tc.wantErr, false, tc.rationale)
	}

	// Existing rows may still grow while at the limit.
	c = Change{Left, "r1", 1}
	step(t, v, c, nil, true, "existing row r1 may grow despite the row limit")
	apply(t, left, right, c)

	if got := v.Snapshot(); reflect.DeepEqual(got, snapBefore) {
		t.Fatalf("expected snapshot to change after a legal insert, still %v", got)
	}
	// Rejections left no trace: replaying the log prefix captured before
	// the rejections must reproduce the pre-rejection snapshot.
	prefix, err := Replay(v.Log(), len(logBefore))
	if err != nil {
		t.Fatalf("replaying pre-rejection log prefix: %v", err)
	}
	if !reflect.DeepEqual(prefix, snapBefore) {
		t.Fatalf("pre-rejection log prefix %v != pre-rejection snapshot %v", prefix, snapBefore)
	}
	checkConsistency(t, v, left, right)
}

func TestErrorSentinelsAreDistinct(t *testing.T) {
	errs := []error{ErrEmptyRow, ErrZeroDelta, ErrDeleteUnderflow, ErrRowLimit}
	for i := range errs {
		for j := range errs {
			if i != j && errors.Is(errs[i], errs[j]) {
				t.Fatalf("error sentinels %v and %v are not distinguishable", errs[i], errs[j])
			}
		}
	}
}

func TestRandomizedAgainstBatch(t *testing.T) {
	rng := rand.New(rand.NewSource(317))
	rows := []string{"a", "b", "c", "d", "e"}

	for trial := 0; trial < 20; trial++ {
		v := New(0)
		left, right := map[string]int{}, map[string]int{}
		for i := 0; i < 200; i++ {
			side := Left
			if rng.Intn(2) == 1 {
				side = Right
			}
			row := rows[rng.Intn(len(rows))]
			delta := 1 + rng.Intn(3)
			if rng.Intn(2) == 1 {
				delta = -delta
			}
			c := Change{side, row, delta}
			_, _, err := v.Apply(c)
			if err != nil {
				if !errors.Is(err, ErrDeleteUnderflow) {
					t.Fatalf("trial %d step %d: unexpected error %v", trial, i, err)
				}
				t.Logf("trial %d step %d: input=%+v -> REJECTED (underflow), state untouched", trial, i, c)
				continue
			}
			apply(t, left, right, c)
		}
		checkConsistency(t, v, left, right)
	}
}

func TestConcurrentAccess(t *testing.T) {
	v := New(0)
	var wg sync.WaitGroup

	// Writers commit disjoint rows so no underflow can occur.
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			row := fmt.Sprintf("row-%d", w)
			for i := 0; i < 50; i++ {
				if _, _, err := v.Apply(Change{Left, row, 1}); err != nil {
					t.Errorf("insert: %v", err)
					return
				}
				if _, _, err := v.Apply(Change{Right, row, 1}); err != nil {
					t.Errorf("right insert: %v", err)
					return
				}
			}
		}(w)
	}
	// Readers run snapshots and self-checks concurrently with commits.
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				snap := v.Snapshot()
				for row, cnt := range snap {
					if cnt <= 0 {
						t.Errorf("snapshot row %q has non-positive multiplicity %d", row, cnt)
						return
					}
				}
				if err := v.SelfCheck(); err != nil {
					t.Errorf("SelfCheck: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	if err := v.SelfCheck(); err != nil {
		t.Fatalf("final SelfCheck: %v", err)
	}
	snap := v.Snapshot()
	for w := 0; w < 4; w++ {
		row := fmt.Sprintf("row-%d", w)
		if snap[row] != 0 {
			t.Fatalf("row %q: got %d, want 0 (balanced inserts)", row, snap[row])
		}
	}
	t.Logf("concurrent run settled: snapshot=%v logLen=%d", snap, len(v.Log()))
}

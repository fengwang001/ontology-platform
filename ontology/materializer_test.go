package ontology

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
)

// testLogger captures audit logs into the buffer and also mirrors them into
// the test output, so every run prints the inputs, result and reasoning.
func testLogger(t *testing.T) (*bytes.Buffer, *Materializer) {
	t.Helper()
	var buf bytes.Buffer
	writer := io.MultiWriter(&buf, testLogWriter{t})
	m := New(WithLogger(writer))
	return &buf, m
}

type testLogWriter struct{ t *testing.T }

func (w testLogWriter) Write(p []byte) (int, error) {
	w.t.Logf("%s", p)
	return len(p), nil
}

func ptr(s string) *string { return &s }

func assertBatchError(t *testing.T, err error, index int, reason error) *BatchError {
	t.Helper()
	var be *BatchError
	if !errors.As(err, &be) {
		t.Fatalf("expected *BatchError, got %T: %v", err, err)
	}
	if be.Index != index {
		t.Fatalf("expected failing index %d, got %d", index, be.Index)
	}
	if !errors.Is(be, reason) {
		t.Fatalf("expected reason %v, got %v", reason, be.Reason)
	}
	return be
}

// Ordered rehearsal: write-then-delete on the same key, and chained
// expectations where a later event observes an earlier event's new value.
func TestApply_OrderingWithinBatch(t *testing.T) {
	_, m := testLogger(t)

	// Same key written then deleted in one batch; the delete sees the value
	// written earlier in the same batch.
	batch := []Event{
		{Key: "k", Op: Write, Value: "v1", Expect: nil},
		{Key: "k", Op: Delete, Expect: ptr("v1")},
		// Chained expectation on another key, satisfied by an earlier event.
		{Key: "a", Op: Write, Value: "1", Expect: nil},
		{Key: "a", Op: Write, Value: "2", Expect: ptr("1")},
		{Key: "a", Op: Write, Value: "3", Expect: ptr("2")},
		// A new key may then be written with an absent-expectation; the batch
		// as a whole must still commit in one step.
		{Key: "b", Op: Write, Value: "depends-on-a", Expect: nil},
	}
	t.Logf("input batch=%s", formatBatch(batch))
	if err := m.Apply(batch); err != nil {
		t.Fatalf("expected success, got %v", err)
	}

	if _, ok := m.Get("k"); ok {
		t.Fatalf("k should have been deleted within the batch, still present")
	}
	if v, ok := m.Get("a"); !ok || v != "3" {
		t.Fatalf("a = %q,%v want 3,true (chained expect)", v, ok)
	}
	if v, ok := m.Get("b"); !ok || v != "depends-on-a" {
		t.Fatalf("b = %q,%v want depends-on-a,true", v, ok)
	}
	if g := m.Generation(); g != 1 {
		t.Fatalf("generation=%d want 1", g)
	}
}

// All-or-nothing: one failing event anywhere rejects the whole batch and the
// committed view is exactly the before-batch view.
func TestApply_AllOrNothing(t *testing.T) {
	_, m := testLogger(t)
	if err := m.Apply([]Event{{Key: "seed", Op: Write, Value: "s", Expect: nil}}); err != nil {
		t.Fatal(err)
	}

	bad := []Event{
		{Key: "x", Op: Write, Value: "x1", Expect: nil},
		{Key: "seed", Op: Delete, Expect: ptr("WRONG")}, // fails: actual s
		{Key: "y", Op: Write, Value: "y1", Expect: nil},
	}
	t.Logf("input batch=%s (second event must fail)", formatBatch(bad))
	err := m.Apply(bad)
	be := assertBatchError(t, err, 1, ErrPrecondition)
	if be.Expected == nil || *be.Expected != "WRONG" {
		t.Fatalf("expected expected=WRONG, got %v", be.Expected)
	}
	if be.Actual == nil || *be.Actual != "s" {
		t.Fatalf("expected actual=s, got %v", be.Actual)
	}

	if _, ok := m.Get("x"); ok {
		t.Fatalf("rejected batch leaked write of x")
	}
	if _, ok := m.Get("y"); ok {
		t.Fatalf("later event of rejected batch was applied")
	}
	if v, ok := m.Get("seed"); !ok || v != "s" {
		t.Fatalf("seed = %q,%v; want s,true (state unchanged)", v, ok)
	}
	if g := m.Generation(); g != 1 {
		t.Fatalf("generation=%d want 1 after rejection", g)
	}

	// Failed materializer keeps working: corrected batch commits.
	good := []Event{
		{Key: "x", Op: Write, Value: "x1", Expect: nil},
		{Key: "seed", Op: Delete, Expect: ptr("s")},
	}
	if err := m.Apply(good); err != nil {
		t.Fatalf("materializer unusable after rejection: %v", err)
	}
	if _, ok := m.Get("seed"); ok {
		t.Fatalf("seed should be deleted by retry batch")
	}
	if g := m.Generation(); g != 2 {
		t.Fatalf("generation=%d want 2", g)
	}
}

// The reported failure always corresponds to the FIRST failing event; later
// failures in the same batch are never reported.
func TestApply_FirstFailureWins(t *testing.T) {
	_, m := testLogger(t)

	batch := []Event{
		{Key: "present", Op: Write, Value: "p", Expect: nil},
		{Key: "absent", Op: Write, Value: "a", Expect: ptr("ghost")}, // fails first
		{Key: "present", Op: Delete, Expect: ptr("WRONG-TOO")},       // also wrong
	}
	err := m.Apply(batch)
	be := assertBatchError(t, err, 1, ErrPrecondition)
	if be.Key != "absent" {
		t.Fatalf("expected key absent at first failure, got %q", be.Key)
	}

	// nil expectation fails when the key is present (earlier in same batch).
	err = m.Apply([]Event{
		{Key: "z", Op: Write, Value: "z1", Expect: nil},
		{Key: "z", Op: Write, Value: "z2", Expect: nil}, // expects absent, but present
	})
	assertBatchError(t, err, 1, ErrPrecondition)
}

// The three illegal-input classes are distinct, decidable errors and leave
// state unchanged.
func TestApply_IllegalInputs(t *testing.T) {
	_, m := testLogger(t)

	if err := m.Apply(nil); !errors.Is(err, ErrEmptyBatch) {
		t.Fatalf("empty batch: want ErrEmptyBatch, got %v", err)
	}
	if err := m.Apply([]Event{}); !errors.Is(err, ErrEmptyBatch) {
		t.Fatalf("empty batch: want ErrEmptyBatch, got %v", err)
	}

	before := m.Snapshot()

	err := m.Apply([]Event{
		{Key: "ok", Op: Write, Value: "1", Expect: nil},
		{Key: "", Op: Write, Value: "bad", Expect: nil},
	})
	assertBatchError(t, err, 1, ErrEmptyKey)

	err = m.Apply([]Event{
		{Key: "q", Op: Op(99), Expect: nil},
	})
	assertBatchError(t, err, 0, ErrInvalidOp)

	after := m.Snapshot()
	if fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatalf("state changed after illegal inputs: before=%v after=%v", before, after)
	}
	if len(after) != 0 {
		t.Fatalf("view not empty after rejected illegal batches: %v", after)
	}
}

// A subsequent valid batch builds on committed earlier batches, proving the
// precondition view stacks real state plus prior committed batches.
func TestApply_MultipleCommittedBatches(t *testing.T) {
	_, m := testLogger(t)
	if err := m.Apply([]Event{{Key: "k", Op: Write, Value: "v1", Expect: nil}}); err != nil {
		t.Fatal(err)
	}
	if err := m.Apply([]Event{{Key: "k", Op: Write, Value: "v2", Expect: ptr("v1")}}); err != nil {
		t.Fatal(err)
	}
	if err := m.Apply([]Event{{Key: "k", Op: Delete, Expect: ptr("v2")}}); err != nil {
		t.Fatal(err)
	}
	if err := m.Apply([]Event{{Key: "k", Op: Write, Value: "v3", Expect: nil}}); err != nil {
		t.Fatal(err)
	}
	if v, ok := m.Get("k"); !ok || v != "v3" {
		t.Fatalf("k=%q,%v want v3,true", v, ok)
	}
}

// Snapshot returns an independent copy of one complete boundary.
func TestSnapshot_IndependentCopy(t *testing.T) {
	_, m := testLogger(t)
	if err := m.Apply([]Event{{Key: "k", Op: Write, Value: "v", Expect: nil}}); err != nil {
		t.Fatal(err)
	}
	snap := m.Snapshot()
	snap["k"] = "tampered"
	snap["other"] = "x"
	if v, ok := m.Get("k"); !ok || v != "v" {
		t.Fatalf("mutation of snapshot leaked into view: %q,%v", v, ok)
	}
}

// Concurrent readers only ever observe complete batch boundaries. Batches
// are designed so that every committed generation is internally coherent;
// any half-batch observation would violate the asserted invariants.
func TestApply_ConcurrentReadersSeeBoundaries(t *testing.T) {
	_, m := testLogger(t)

	const batchesN = 200
	batches := make([][]Event, batchesN)
	for g := 1; g <= batchesN; g++ {
		prev := fmt.Sprintf("v%d", g-1)
		cur := fmt.Sprintf("v%d", g)
		if g == 1 {
			batches[g-1] = []Event{
				{Key: "k1", Op: Write, Value: cur, Expect: nil},
				{Key: "k2", Op: Write, Value: cur, Expect: nil},
				{Key: "k3", Op: Write, Value: cur, Expect: nil},
			}
		} else {
			batches[g-1] = []Event{
				{Key: "k1", Op: Write, Value: cur, Expect: ptr(prev)},
				{Key: "k2", Op: Write, Value: cur, Expect: ptr(prev)},
				{Key: "k3", Op: Write, Value: cur, Expect: ptr(prev)},
			}
		}
	}

	var readers sync.WaitGroup
	stop := make(chan struct{})

	readOnce := func() string {
		snap := m.Snapshot()
		n := len(snap)
		switch {
		case n == 0:
			return "" // complete boundary before batch 1
		case n != 3:
			t.Errorf("observed half batch: len=%d snapshot=%v", n, snap)
			return ""
		}
		v1, v2, v3 := snap["k1"], snap["k2"], snap["k3"]
		if v1 != v2 || v2 != v3 {
			t.Errorf("torn read across batch boundary: %v", snap)
		}
		return v1
	}

	for r := 0; r < 8; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					readOnce()
					if err := m.Check(); err != nil {
						t.Errorf("check failed concurrently: %v", err)
					}
				}
			}
		}()
	}

	for _, b := range batches {
		if err := m.Apply(b); err != nil {
			t.Fatalf("commit failed: %v", err)
		}
	}
	close(stop)
	readers.Wait()

	if g := m.Generation(); g != batchesN {
		t.Fatalf("generation=%d want %d", g, batchesN)
	}
	final := readOnce()
	if final != fmt.Sprintf("v%d", batchesN) {
		t.Fatalf("final value=%q want v%d", final, batchesN)
	}
}

// Applies serialized concurrently must not corrupt generations or views.
func TestApply_ConcurrentAppliers(t *testing.T) {
	_, m := testLogger(t)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			key := fmt.Sprintf("w%d", id)
			for i := 0; i < 20; i++ {
				exp := m.Snapshot()[key]
				batch := []Event{{Key: key, Op: Write, Value: fmt.Sprintf("%d", i)}}
				if i == 0 {
					batch[0].Expect = nil
				} else {
					batch[0].Expect = ptr(exp)
				}
				// Precondition may be lost due to interleaving with other
				// iterations of the same worker; retries must converge.
				for err := m.Apply(batch); err != nil; err = m.Apply(batch) {
					if !errors.Is(err, ErrPrecondition) {
						t.Errorf("unexpected error: %v", err)
						return
					}
					cur := m.Snapshot()[key]
					batch[0].Expect = ptr(cur)
				}
			}
		}(w)
	}
	wg.Wait()
	snap := m.Snapshot()
	if len(snap) != 8 {
		t.Fatalf("want 8 worker keys, got %d: %v", len(snap), snap)
	}
	if err := m.Check(); err != nil {
		t.Fatalf("final check: %v", err)
	}
}

package versioned

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"testing"
)

func w(key string, version int64, value string) *Event {
	return &Event{Key: key, Version: version, Op: OpWrite, Value: []byte(value)}
}

func d(key string, version int64) *Event {
	return &Event{Key: key, Version: version, Op: OpDelete}
}

func mustCommit(t *testing.T, s *Store, events []*Event) {
	t.Helper()
	if err := s.Commit(events); err != nil {
		t.Fatalf("Commit unexpected error: %v", err)
	}
}

func assertMissing(t *testing.T, s *Store, key string) {
	t.Helper()
	if _, ok := s.Get(key); ok {
		t.Fatalf("key %q unexpectedly present", key)
	}
}

func assertRow(t *testing.T, s *Store, key, value string, version int64) {
	t.Helper()
	row, ok := s.Get(key)
	if !ok {
		t.Fatalf("key %q unexpectedly missing", key)
	}
	if row.Version != version || string(row.Value) != value {
		t.Fatalf("key %q = @%d=%q, want @%d=%q", key, row.Version, string(row.Value), version, value)
	}
}

// An event whose version equals the current version must be ignored.
func TestEqualVersionIgnored(t *testing.T) {
	s, err := New(100, 10)
	if err != nil {
		t.Fatal(err)
	}
	mustCommit(t, s, []*Event{w("a", 3, "first")})
	assertRow(t, s, "a", "first", 3)

	mustCommit(t, s, []*Event{w("a", 3, "equal-version")})
	assertRow(t, s, "a", "first", 3)

	mustCommit(t, s, []*Event{d("a", 5)})
	assertMissing(t, s, "a")
	mustCommit(t, s, []*Event{d("a", 5)})
	if tv, ok := s.TombstoneVersion("a"); !ok || tv != 5 {
		t.Fatalf("tombstone = (%d,%v), want (5,true)", tv, ok)
	}

	_, ignored := s.Counts()
	if ignored != 2 {
		t.Fatalf("ignored = %d, want 2", ignored)
	}
	if err := s.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// A late write with version not above the tombstone is blocked; the key
// never shows a live row while the tombstone exists.
func TestOldWriteBlockedByTombstone(t *testing.T) {
	s, err := New(100, 10)
	if err != nil {
		t.Fatal(err)
	}
	mustCommit(t, s, []*Event{w("k", 1, "old")})
	mustCommit(t, s, []*Event{d("k", 5)})
	assertMissing(t, s, "k")
	if tv, ok := s.TombstoneVersion("k"); !ok || tv != 5 {
		t.Fatalf("tombstone = (%d,%v), want (5,true)", tv, ok)
	}

	for _, v := range []int64{1, 3, 5} {
		mustCommit(t, s, []*Event{w("k", v, "late")})
		assertMissing(t, s, "k")
		if _, ok := s.TombstoneVersion("k"); !ok {
			t.Fatalf("tombstone for %q lost after late write v%d", "k", v)
		}
	}

	mustCommit(t, s, []*Event{w("k", 6, "new")})
	assertRow(t, s, "k", "new", 6)
	if _, ok := s.TombstoneVersion("k"); ok {
		t.Fatalf("tombstone for %q unexpectedly retained", "k")
	}

	applied, ignored := s.Counts()
	if applied != 3 || ignored != 3 {
		t.Fatalf("counts applied=%d ignored=%d, want 3/3", applied, ignored)
	}
	if err := s.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// Deleting a key that never existed still creates a tombstone.
func TestDeleteAbsentKeyCreatesTombstone(t *testing.T) {
	s, err := New(100, 10)
	if err != nil {
		t.Fatal(err)
	}
	mustCommit(t, s, []*Event{d("ghost", 4)})
	if tv, ok := s.TombstoneVersion("ghost"); !ok || tv != 4 {
		t.Fatalf("tombstone = (%d,%v), want (4,true)", tv, ok)
	}
	mustCommit(t, s, []*Event{w("ghost", 4, "same-version")})
	assertMissing(t, s, "ghost")
}

// Tombstones clear once watermark-tombVersion reaches retention; afterwards
// the key has no state and a positive-version write applies.
func TestTombstoneExpiryCleared(t *testing.T) {
	s, err := New(3, 10)
	if err != nil {
		t.Fatal(err)
	}
	mustCommit(t, s, []*Event{d("k", 5)})

	mustCommit(t, s, []*Event{w("other", 7, "x")}) // age 2 < 3
	if tv, ok := s.TombstoneVersion("k"); !ok || tv != 5 {
		t.Fatalf("tombstone = (%d,%v), want retained at 5", tv, ok)
	}

	mustCommit(t, s, []*Event{w("other", 8, "y")}) // age 3 == 3
	if _, ok := s.TombstoneVersion("k"); ok {
		t.Fatalf("tombstone for %q should have been cleared", "k")
	}
	if _, ok := s.Get("k"); ok {
		t.Fatalf("key %q should have no live row after GC", "k")
	}

	mustCommit(t, s, []*Event{w("k", 1, "after-gc")})
	assertRow(t, s, "k", "after-gc", 1)

	if s.Watermark() != 8 {
		t.Fatalf("watermark = %d, want 8", s.Watermark())
	}
	if err := s.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// Retention 0 clears every tombstone immediately: the delete itself raises
// the watermark to its version.
func TestRetentionZeroClearsImmediately(t *testing.T) {
	s, err := New(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	mustCommit(t, s, []*Event{w("k", 9, "v")})
	mustCommit(t, s, []*Event{d("k", 10)})
	if _, ok := s.TombstoneVersion("k"); ok {
		t.Fatalf("tombstone should be cleared with retention 0")
	}
}

// The watermark tracks every accepted event, including ignored ones.
func TestWatermarkIncludesIgnoredEvents(t *testing.T) {
	s, err := New(100, 10)
	if err != nil {
		t.Fatal(err)
	}
	mustCommit(t, s, []*Event{d("k", 5)})
	mustCommit(t, s, []*Event{w("k", 2, "late")})
	if s.Watermark() != 5 {
		t.Fatalf("watermark = %d, want 5", s.Watermark())
	}
	mustCommit(t, s, []*Event{w("x", 20, "y")})
	if s.Watermark() != 20 {
		t.Fatalf("watermark = %d, want 20", s.Watermark())
	}
}

// Every rejection reason is distinct, classifiable via errors.Is, and a
// rejected batch leaves no trace.
func TestInvalidInputRejectsWholeBatchWithoutTrace(t *testing.T) {
	tests := []struct {
		name   string
		new    func() (*Store, error)
		events []*Event
		reason error
	}{
		{"negative retention", func() (*Store, error) { return New(-1, 10) }, nil, ErrInvalidRetention},
		{"zero batch limit", func() (*Store, error) { return New(1, 0) }, nil, ErrInvalidBatchLimit},
		{"batch too large", func() (*Store, error) { return New(1, 2) },
			[]*Event{w("a", 1, "x"), w("b", 2, "y"), w("c", 3, "z")}, ErrBatchTooLarge},
		{"nil event", func() (*Store, error) { return New(1, 10) }, []*Event{nil}, ErrNilEvent},
		{"empty key", func() (*Store, error) { return New(1, 10) }, []*Event{w("", 1, "x")}, ErrEmptyKey},
		{"zero version", func() (*Store, error) { return New(1, 10) }, []*Event{w("a", 0, "x")}, ErrInvalidVersion},
		{"negative version", func() (*Store, error) { return New(1, 10) }, []*Event{d("a", -2)}, ErrInvalidVersion},
		{"invalid op", func() (*Store, error) { return New(1, 10) },
			[]*Event{{Key: "a", Version: 1, Op: Op(99)}}, ErrInvalidOp},
		{"nil write value", func() (*Store, error) { return New(1, 10) },
			[]*Event{{Key: "a", Version: 1, Op: OpWrite, Value: nil}}, ErrNilValue},
		{"duplicate key", func() (*Store, error) { return New(1, 10) },
			[]*Event{w("a", 1, "x"), w("a", 2, "y")}, ErrDuplicateKey},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, err := tc.new()
			if tc.events == nil {
				if !errors.Is(err, tc.reason) {
					t.Fatalf("New error = %v, want reason %v", err, tc.reason)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			before := s.Snapshot()
			err = s.Commit(tc.events)
			if !errors.Is(err, tc.reason) {
				t.Fatalf("Commit error = %v, want reason %v", err, tc.reason)
			}
			var be *BatchError
			if !errors.As(err, &be) {
				t.Fatalf("error %v is not *BatchError", err)
			}
			if !snapshotsEqual(before, s.Snapshot()) {
				t.Fatalf("state changed after rejection: before=%+v after=%+v", before, s.Snapshot())
			}
			if err := s.SelfCheck(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// A valid first event followed by an invalid second event applies neither.
func TestBatchRejectionIsAtomic(t *testing.T) {
	s, err := New(100, 10)
	if err != nil {
		t.Fatal(err)
	}
	err = s.Commit([]*Event{w("a", 1, "x"), {Key: "b", Version: 0, Op: OpWrite, Value: []byte("y")}})
	if !errors.Is(err, ErrInvalidVersion) {
		t.Fatalf("error = %v, want ErrInvalidVersion", err)
	}
	if _, ok := s.Get("a"); ok {
		t.Fatalf("first event must not apply when the batch is rejected")
	}
	if s.Watermark() != 0 {
		t.Fatalf("watermark = %d, want 0", s.Watermark())
	}
	applied, ignored := s.Counts()
	if applied != 0 || ignored != 0 {
		t.Fatalf("counts applied=%d ignored=%d, want 0/0", applied, ignored)
	}
}

func snapshotsEqual(a, b Snapshot) bool {
	if a.Watermark != b.Watermark || a.Applied != b.Applied || a.Ignored != b.Ignored {
		return false
	}
	if len(a.Live) != len(b.Live) || len(a.Tombs) != len(b.Tombs) {
		return false
	}
	for key, ra := range a.Live {
		rb, ok := b.Live[key]
		if !ok || ra.Version != rb.Version || !bytes.Equal(ra.Value, rb.Value) {
			return false
		}
	}
	for key, tv := range a.Tombs {
		if b.Tombs[key] != tv {
			return false
		}
	}
	return true
}

// Queries and self-checks run while commits are in flight; -race verifies
// safe concurrency and SelfCheck must never observe corruption.
func TestConcurrentQueriesAndCommits(t *testing.T) {
	s, err := New(5, 100)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(uint64(id), 1))
			for i := 0; i < 300; i++ {
				key := fmt.Sprintf("k%d", rng.IntN(8))
				version := int64(i + 1)
				var event *Event
				if i%3 == 0 {
					event = d(key, version)
				} else {
					event = w(key, version, fmt.Sprintf("v%d", version))
				}
				if err := s.Commit([]*Event{event}); err != nil {
					t.Errorf("commit: %v", err)
					return
				}
			}
		}(worker)
	}

	for reader := 0; reader < 4; reader++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(uint64(id), 99))
			for i := 0; i < 300; i++ {
				key := fmt.Sprintf("k%d", rng.IntN(8))
				_, _ = s.Get(key)
				_, _ = s.TombstoneVersion(key)
				_ = s.Watermark()
				_, _ = s.Counts()
				_ = s.Snapshot()
				if err := s.SelfCheck(); err != nil {
					t.Errorf("self-check: %v", err)
					return
				}
			}
		}(reader)
	}

	wg.Wait()
	close(stop)
	if err := s.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// Deterministic out-of-order scenario: the GC store with a large retention
// (so GC never fires within the scenario) matches the never-collecting
// reference exactly.
func TestReferenceEquivalenceDeterministic(t *testing.T) {
	s, err := New(1000, 50)
	if err != nil {
		t.Fatal(err)
	}
	ref := NewReference()

	// Batches are valid (distinct keys inside a batch); events are shuffled
	// relative to their versions.
	stream := [][]*Event{
		{w("a", 10, "a10"), w("b", 2, "b2"), d("c", 7)},
		{w("a", 5, "a5-stale"), d("b", 9), w("c", 6, "c6-stale")},
		{w("b", 11, "b11"), d("a", 12), w("d", 1, "d1")},
		{w("c", 8, "c8"), w("a", 13, "a13"), d("d", 3)},
		{w("d", 2, "d2-stale"), d("c", 14), w("a", 11, "a11-stale")},
	}
	for _, batch := range stream {
		if err := s.Commit(batch); err != nil {
			t.Fatal(err)
		}
		ref.Commit(batch)
		if !liveRowsEqual(s.Snapshot().Live, ref.LiveSnapshot()) {
			t.Fatalf("live rows diverged after batch %v:\nstore=%v\nref =%v", batch, s.Snapshot().Live, ref.LiveSnapshot())
		}
	}
	if err := s.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// Randomized bounded-lateness stream: when no event is more than
// retention-window late, tombstone collection is invisible and the GC store's
// live rows always match the never-collecting reference.
func TestReferenceEquivalenceBoundedLateness(t *testing.T) {
	const (
		versions   = 600
		numKeys    = 6
		window     = 3 // max swap distance
		retention  = 2*window + 2
		batchLimit = numKeys
	)

	s, err := New(retention, batchLimit)
	if err != nil {
		t.Fatal(err)
	}
	ref := NewReference()
	rng := rand.New(rand.NewPCG(42, 7))

	// Build an event per version, then reorder via bounded-distance swaps so
	// every event arrives at most `window` positions late.
	events := make([]*Event, versions)
	for i := range events {
		key := fmt.Sprintf("k%d", rng.IntN(numKeys))
		version := int64(i + 1)
		if rng.IntN(3) == 0 {
			events[i] = d(key, version)
		} else {
			events[i] = w(key, version, fmt.Sprintf("v%d", version))
		}
	}
	for range 200 {
		i := rng.IntN(versions)
		j := i + rng.IntN(window+1)
		if j < versions {
			events[i], events[j] = events[j], events[i]
		}
	}

	// Feed in consecutive batches; split batches so each key appears once.
	var batch []*Event
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := s.Commit(batch); err != nil {
			t.Fatalf("commit %v: %v", batch, err)
		}
		ref.Commit(batch)
		if !liveRowsEqual(s.Snapshot().Live, ref.LiveSnapshot()) {
			t.Fatalf("live rows diverged:\nstore=%v\nref =%v", s.Snapshot().Live, ref.LiveSnapshot())
		}
		batch = batch[:0]
	}
	seen := map[string]bool{}
	for _, e := range events {
		if seen[e.Key] {
			flush()
			seen = map[string]bool{}
		}
		seen[e.Key] = true
		batch = append(batch, e)
	}
	flush()

	if err := s.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// Demonstrates the documented premise: once a tombstone is collected, an
// older write resurrects the key in the GC store while the never-collecting
// reference keeps blocking it. This is exactly why equivalence only holds
// for bounded-lateness streams.
func TestReferenceDivergenceWhenPremiseBroken(t *testing.T) {
	s, err := New(2, 10)
	if err != nil {
		t.Fatal(err)
	}
	ref := NewReference()

	stream := []*Event{d("k", 5), w("other", 7, "x"), w("k", 1, "zombie")}
	for _, e := range stream {
		if err := s.Commit([]*Event{e}); err != nil {
			t.Fatal(err)
		}
		ref.Commit([]*Event{e})
	}
	if _, ok := s.Get("k"); !ok {
		t.Fatalf("expected stale write to resurrect %q after GC", "k")
	}
	if _, ok := ref.Get("k"); ok {
		t.Fatalf("reference with permanent tombstone must keep blocking %q", "k")
	}
}

func liveRowsEqual(a, b map[string]Row) bool {
	if len(a) != len(b) {
		return false
	}
	for key, ra := range a {
		rb, ok := b[key]
		if !ok || ra.Version != rb.Version || !bytes.Equal(ra.Value, rb.Value) {
			return false
		}
	}
	return true
}

// Logs contain each step's input, live rows and the decision rationale.
func TestDecisionLogging(t *testing.T) {
	var buf bytes.Buffer
	s, err := New(3, 10, WithLogger(newLineLogger(&buf)))
	if err != nil {
		t.Fatal(err)
	}

	mustCommit(t, s, []*Event{w("a", 1, "one"), d("b", 2)})
	mustCommit(t, s, []*Event{w("a", 1, "dup")}) // ignored, equal version
	mustCommit(t, s, []*Event{d("a", 5)})
	mustCommit(t, s, []*Event{w("a", 4, "late")})  // blocked by tombstone
	mustCommit(t, s, []*Event{w("c", 8, "eight")}) // age of tombstone a: 3 => GC

	log := buf.String()
	for _, want := range []string{
		`input key="a" op=write version=1`,
		"=> APPLY write",
		"no state",
		"1 <= 1 => IGNORE",
		"live row",
		"=> APPLY delete (row removed, tombstone set)",
		"tombstone",
		"4 <= 5 => IGNORE",
		"tombstone GC: key=\"a\"",
		"batch end: watermark=8",
		"live rows:",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q\nfull log:\n%s", want, log)
		}
	}
	t.Logf("full decision log:\n%s", log)
}

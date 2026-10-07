package bulkimport_test

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"ontology/bulkimport"
)

// badEntryValidator fails entries whose Fields["bad"] == "1".
func badEntryValidator(e bulkimport.Entry) error {
	if e.Fields["bad"] == "1" {
		return errors.New("entry marked bad")
	}
	return nil
}

type countingSink struct {
	mu   sync.Mutex
	puts []string
	fail map[string]bool
}

func (s *countingSink) Put(jobID string, e bulkimport.Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail[e.ID] {
		return errors.New("sink failure")
	}
	s.puts = append(s.puts, e.ID)
	return nil
}

func (s *countingSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.puts)
}

func newTestManager() (*bulkimport.Manager, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return bulkimport.NewManager(bulkimport.WithLogger(logger)), buf
}

func mustCreate(t *testing.T, m *bulkimport.Manager, cfg bulkimport.JobConfig) {
	t.Helper()
	if err := m.CreateJob(cfg); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
}

// TestDanglingIndexCostIndependentOfProcessedEntries proves that
// dangling-reference bookkeeping does not grow with the number of
// processed entries: the engine never scans the entry table
// (EntryTableScans == 0) and resolving a reference touches exactly the
// entries waiting on it (WaiterPops == number of waiters), no matter
// how many entries were processed before.
func TestDanglingIndexCostIndependentOfProcessedEntries(t *testing.T) {
	for _, total := range []int{1_000, 20_000} {
		m, _ := newTestManager()
		mustCreate(t, m, bulkimport.JobConfig{ID: "j", Validator: badEntryValidator})

		// Land `total` entries without any references.
		const perChunk = 500
		for seq := 0; seq < total/perChunk; seq++ {
			var entries []bulkimport.Entry
			for i := 0; i < perChunk; i++ {
				entries = append(entries, entry(fmt.Sprintf("e%06d", seq*perChunk+i)))
			}
			mustSubmit(t, m, chunk("j", seq, entries...))
		}

		// Five waiters dangle on five not-yet-declared targets.
		mustSubmit(t, m, chunk("j", total/perChunk,
			entry("w0", "t0"), entry("w1", "t1"), entry("w2", "t2"),
			entry("w3", "t3"), entry("w4", "t4")))
		// The targets arrive and resolve the waiters.
		mustSubmit(t, m, chunk("j", total/perChunk+1,
			entry("t0"), entry("t1"), entry("t2"), entry("t3"), entry("t4")))

		stats, err := m.Stats("j")
		if err != nil {
			t.Fatal(err)
		}
		if stats.EntryTableScans != 0 {
			t.Fatalf("total=%d: EntryTableScans = %d, engine must never scan the entry table",
				total, stats.EntryTableScans)
		}
		if stats.WaiterPops != 5 {
			t.Fatalf("total=%d: WaiterPops = %d, want exactly 5 (one per waiter, independent of total)",
				total, stats.WaiterPops)
		}
		if stats.PendingEntries != 0 || stats.DanglingRefs != 0 {
			t.Fatalf("total=%d: stats = %+v, want no leftover dangling state", total, stats)
		}
		if stats.LandedEntries != total+10 {
			t.Fatalf("total=%d: LandedEntries = %d, want %d", total, stats.LandedEntries, total+10)
		}
	}
}

// TestCloseRejectsNewChunks covers early close: accepted chunks finish
// processing, new chunks are rejected, and landed entries are
// unaffected.
func TestCloseRejectsNewChunks(t *testing.T) {
	m, _ := newTestManager()
	mustCreate(t, m, bulkimport.JobConfig{ID: "j", Validator: badEntryValidator})

	mustSubmit(t, m, chunk("j", 0, entry("a", "missing"), entry("landed")))
	if err := m.CloseJob("j"); err != nil {
		t.Fatal(err)
	}
	res := mustSubmit(t, m, chunk("j", 1, entry("b")))
	if res.Outcome != bulkimport.ChunkRejected || res.Err.Kind != bulkimport.ErrKindJobClosed {
		t.Fatalf("submit after close = %+v, want rejected/job_closed", res)
	}
	if v := mustQuery(t, m, "j", "landed"); v.Status != bulkimport.EntryLanded {
		t.Fatalf("landed entry view = %+v, want landed (unaffected by close)", v)
	}
	if v := mustQuery(t, m, "j", "a"); v.Status != bulkimport.EntryPending {
		t.Fatalf("pending entry view = %+v, want still pending (close does not finalize)", v)
	}
	if v := mustQuery(t, m, "j", "b"); v.Status != bulkimport.EntryUnknown {
		t.Fatalf("rejected entry view = %+v, want unknown", v)
	}
}

// TestCloseSubmitRaceDeterminism races CloseJob against SubmitChunk:
// the outcome must always equal one of the two sequential orders, never
// a partial state.
func TestCloseSubmitRaceDeterminism(t *testing.T) {
	for i := 0; i < 200; i++ {
		m, _ := newTestManager()
		mustCreate(t, m, bulkimport.JobConfig{ID: "j", Validator: badEntryValidator})

		var wg sync.WaitGroup
		var res bulkimport.ChunkResult
		wg.Add(2)
		go func() { defer wg.Done(); res = mustSubmit(t, m, chunk("j", 0, entry("a"))) }()
		go func() { defer wg.Done(); _ = m.CloseJob("j") }()
		wg.Wait()

		v := mustQuery(t, m, "j", "a")
		switch res.Outcome {
		case bulkimport.ChunkAccepted:
			// Submit won the race: the chunk is fully processed.
			if v.Status != bulkimport.EntryLanded {
				t.Fatalf("iter %d: accepted chunk but entry = %+v", i, v)
			}
		case bulkimport.ChunkRejected:
			// Close won the race: rejection must be job_closed and the
			// entry must not exist.
			if res.Err.Kind != bulkimport.ErrKindJobClosed {
				t.Fatalf("iter %d: rejected with %v, want job_closed", i, res.Err.Kind)
			}
			if v.Status != bulkimport.EntryUnknown {
				t.Fatalf("iter %d: rejected chunk but entry = %+v", i, v)
			}
		default:
			t.Fatalf("iter %d: unexpected outcome %v", i, res.Outcome)
		}
	}
}

// TestParallelIndependentChunks submits many independent chunks
// concurrently; all entries must land and results must not interfere.
func TestParallelIndependentChunks(t *testing.T) {
	m, _ := newTestManager()
	mustCreate(t, m, bulkimport.JobConfig{ID: "j", Validator: badEntryValidator})

	const n = 32
	var wg sync.WaitGroup
	results := make([]bulkimport.ChunkResult, n)
	wg.Add(n)
	for seq := 0; seq < n; seq++ {
		go func(seq int) {
			defer wg.Done()
			res, err := m.SubmitChunk(chunk("j", seq,
				entry(fmt.Sprintf("x%02d-a", seq)), entry(fmt.Sprintf("x%02d-b", seq))))
			if err != nil {
				t.Error(err)
				return
			}
			results[seq] = res
		}(seq)
	}
	wg.Wait()

	for seq, res := range results {
		if res.Outcome != bulkimport.ChunkAccepted {
			t.Fatalf("chunk %d outcome = %v", seq, res.Outcome)
		}
		for _, er := range res.Entries {
			if er.Status != bulkimport.EntryLanded {
				t.Fatalf("chunk %d entry %s = %v, want landed", seq, er.ID, er.Status)
			}
		}
	}
	stats, err := m.Stats("j")
	if err != nil {
		t.Fatal(err)
	}
	if stats.LandedEntries != 2*n || stats.ArrivedChunks != n {
		t.Fatalf("stats = %+v, want %d landed entries in %d chunks", stats, 2*n, n)
	}
}

// TestErrorPriority covers the fixed error priority when several
// conditions hold at once.
func TestErrorPriority(t *testing.T) {
	m, _ := newTestManager()
	mustCreate(t, m, bulkimport.JobConfig{ID: "j", MaxChunks: 3, Validator: badEntryValidator})

	// ref_failed (2) outranks validation (4): "bad" fails its own
	// validation AND references the already-failed "x".
	mustSubmit(t, m, chunk("j", 0, badEntry("x")))
	res := mustSubmit(t, m, chunk("j", 1, badEntry("bad", "x")))
	if res.Entries[0].Status != bulkimport.EntryFailed ||
		res.Entries[0].Err.Kind != bulkimport.ErrKindRefFailed {
		t.Fatalf("entry bad result = %+v, want failed/ref_failed (priority over validation)", res.Entries[0])
	}

	// chunk_conflict (1) outranks job_closed (5): a conflicting
	// redelivery after close still reports the conflict.
	if err := m.CloseJob("j"); err != nil {
		t.Fatal(err)
	}
	res2 := mustSubmit(t, m, chunk("j", 0, entry("x", "changed")))
	if res2.Outcome != bulkimport.ChunkRejected || res2.Err.Kind != bulkimport.ErrKindChunkConflict {
		t.Fatalf("conflicting chunk after close = %+v, want rejected/chunk_conflict", res2)
	}
	// A brand-new chunk after close reports job_closed.
	res3 := mustSubmit(t, m, chunk("j", 2, entry("new")))
	if res3.Outcome != bulkimport.ChunkRejected || res3.Err.Kind != bulkimport.ErrKindJobClosed {
		t.Fatalf("new chunk after close = %+v, want rejected/job_closed", res3)
	}
}

// TestQueryDoesNotMutate verifies that querying is a pure read.
func TestQueryDoesNotMutate(t *testing.T) {
	m, _ := newTestManager()
	mustCreate(t, m, bulkimport.JobConfig{ID: "j", Validator: badEntryValidator})
	mustSubmit(t, m, chunk("j", 0, entry("a", "missing")))

	before, err := m.Stats("j")
	if err != nil {
		t.Fatal(err)
	}
	v1 := mustQuery(t, m, "j", "a")
	v2 := mustQuery(t, m, "j", "a")
	mustQuery(t, m, "j", "never-seen")
	after, err := m.Stats("j")
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("stats changed by queries: before=%+v after=%+v", before, after)
	}
	if v1.Status != v2.Status || len(v1.WaitingOn) != len(v2.WaitingOn) {
		t.Fatalf("repeated queries differ: %+v vs %+v", v1, v2)
	}
}

// TestLoggingDecisions verifies that every chunk and entry decision is
// logged with its input, outcome and reason.
func TestLoggingDecisions(t *testing.T) {
	m, buf := newTestManager()
	mustCreate(t, m, bulkimport.JobConfig{ID: "jlog", MaxChunks: 2, Validator: badEntryValidator})

	mustSubmit(t, m, chunk("jlog", 0, entry("a", "b")))
	mustSubmit(t, m, chunk("jlog", 1, badEntry("b")))
	mustSubmit(t, m, chunk("jlog", 1, badEntry("b"))) // duplicate

	out := buf.String()
	for _, want := range []string{
		`job=jlog`, `seq=0`, `seq=1`,
		"entry pending on dangling references", `entry=a`, `waitingOn=[b]`,
		"entry failed", `entry=b`, "kind=validation",
		"referenced entry deterministically failed",
		"chunk duplicate, entries not reprocessed",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("log missing %q\nlog:\n%s", want, out)
		}
	}
}

// TestSelfReference covers an entry referencing itself: a cycle of one.
func TestSelfReference(t *testing.T) {
	m, _ := newTestManager()
	mustCreate(t, m, bulkimport.JobConfig{ID: "j", MaxChunks: 1, Validator: badEntryValidator})
	res := mustSubmit(t, m, chunk("j", 0, entry("a", "a")))
	if res.Entries[0].Status != bulkimport.EntryFailed ||
		res.Entries[0].Err.Kind != bulkimport.ErrKindValidation {
		t.Fatalf("self-referencing entry = %+v, want failed/validation (circular)", res.Entries[0])
	}
}

// TestEntryRedeclaration covers the same entry ID appearing in two
// different chunks: the first declaration wins, the second fails as a
// validation error, and references bind to the first.
func TestEntryRedeclaration(t *testing.T) {
	m, _ := newTestManager()
	mustCreate(t, m, bulkimport.JobConfig{ID: "j", Validator: badEntryValidator})

	mustSubmit(t, m, chunk("j", 0, entry("a")))
	res := mustSubmit(t, m, chunk("j", 1, entry("a"), entry("r", "a")))
	if res.Entries[0].Status != bulkimport.EntryFailed ||
		res.Entries[0].Err.Kind != bulkimport.ErrKindValidation {
		t.Fatalf("redeclared entry = %+v, want failed/validation", res.Entries[0])
	}
	if v := mustQuery(t, m, "j", "a"); v.Status != bulkimport.EntryLanded {
		t.Fatalf("first declaration view = %+v, want landed (unaffected)", v)
	}
	if v := mustQuery(t, m, "j", "r"); v.Status != bulkimport.EntryLanded {
		t.Fatalf("referencing entry view = %+v, want landed (bound to first declaration)", v)
	}
}

// TestSinkFailureFailsEntry covers sink rejection at landing time.
func TestSinkFailureFailsEntry(t *testing.T) {
	m, _ := newTestManager()
	sink := &countingSink{fail: map[string]bool{"a": true}}
	mustCreate(t, m, bulkimport.JobConfig{ID: "j", Validator: badEntryValidator, Sink: sink})
	res := mustSubmit(t, m, chunk("j", 0, entry("a")))
	if res.Entries[0].Status != bulkimport.EntryFailed ||
		res.Entries[0].Err.Kind != bulkimport.ErrKindValidation {
		t.Fatalf("sink-rejected entry = %+v, want failed/validation", res.Entries[0])
	}
}

// TestValidationDeferredUntilRefsResolve covers the declarative error
// priority: an entry that fails its own validation but still has
// unresolved references stays pending, so that a higher-priority
// outcome (ref_failed or dangling_timeout) can still win. The final
// state is independent of chunk arrival order.
func TestValidationDeferredUntilRefsResolve(t *testing.T) {
	// Reference lands: the validation error materializes.
	m, _ := newTestManager()
	mustCreate(t, m, bulkimport.JobConfig{ID: "j1", Validator: badEntryValidator})
	mustSubmit(t, m, chunk("j1", 0, badEntry("a", "b")))
	if v := mustQuery(t, m, "j1", "a"); v.Status != bulkimport.EntryPending {
		t.Fatalf("bad entry with dangling ref = %+v, want pending (validation deferred)", v)
	}
	mustSubmit(t, m, chunk("j1", 1, entry("b")))
	if v := mustQuery(t, m, "j1", "a"); v.Status != bulkimport.EntryFailed ||
		v.Err.Kind != bulkimport.ErrKindValidation {
		t.Fatalf("after ref landed = %+v, want failed/validation", v)
	}

	// Reference fails: ref_failed outranks the validation error.
	m2, _ := newTestManager()
	mustCreate(t, m2, bulkimport.JobConfig{ID: "j2", Validator: badEntryValidator})
	mustSubmit(t, m2, chunk("j2", 0, badEntry("a", "b")))
	mustSubmit(t, m2, chunk("j2", 1, badEntry("b")))
	if v := mustQuery(t, m2, "j2", "a"); v.Status != bulkimport.EntryFailed ||
		v.Err.Kind != bulkimport.ErrKindRefFailed {
		t.Fatalf("after ref failed = %+v, want failed/ref_failed", v)
	}

	// Chunk limit hit first: dangling_timeout outranks the validation
	// error.
	m3, _ := newTestManager()
	mustCreate(t, m3, bulkimport.JobConfig{ID: "j3", MaxChunks: 1, Validator: badEntryValidator})
	mustSubmit(t, m3, chunk("j3", 0, badEntry("a", "ghost")))
	if v := mustQuery(t, m3, "j3", "a"); v.Status != bulkimport.EntryTimedOut ||
		v.Err.Kind != bulkimport.ErrKindDanglingTimeout {
		t.Fatalf("after chunk limit = %+v, want timed_out/dangling_timeout", v)
	}
}

// TestErrorKindPriorityOrder pins the required fixed priority order.
func TestErrorKindPriorityOrder(t *testing.T) {
	want := []bulkimport.ErrorKind{
		bulkimport.ErrKindChunkConflict,
		bulkimport.ErrKindRefFailed,
		bulkimport.ErrKindDanglingTimeout,
		bulkimport.ErrKindValidation,
		bulkimport.ErrKindJobClosed,
	}
	for i := 1; i < len(want); i++ {
		if want[i-1].Priority() >= want[i].Priority() {
			t.Fatalf("priority order violated: %v must outrank %v", want[i-1], want[i])
		}
	}
}

func mustSubmit(t *testing.T, m *bulkimport.Manager, c bulkimport.Chunk) bulkimport.ChunkResult {
	t.Helper()
	res, err := m.SubmitChunk(c)
	if err != nil {
		t.Fatalf("SubmitChunk(seq=%d): %v", c.Seq, err)
	}
	return res
}

func mustQuery(t *testing.T, m *bulkimport.Manager, jobID, entryID string) bulkimport.EntryView {
	t.Helper()
	v, err := m.QueryEntry(jobID, entryID)
	if err != nil {
		t.Fatalf("QueryEntry(%q): %v", entryID, err)
	}
	return v
}

func entry(id string, refs ...string) bulkimport.Entry {
	return bulkimport.Entry{ID: id, Fields: map[string]string{"name": id}, Refs: refs}
}

func badEntry(id string, refs ...string) bulkimport.Entry {
	return bulkimport.Entry{ID: id, Fields: map[string]string{"bad": "1"}, Refs: refs}
}

func chunk(jobID string, seq int, entries ...bulkimport.Entry) bulkimport.Chunk {
	return bulkimport.Chunk{JobID: jobID, Seq: seq, Entries: entries}
}

// TestCrossChunkDanglingThenLand covers recording a dangling reference
// whose target lives in a not-yet-arrived chunk, and the automatic
// retry that lands the waiter once the target chunk arrives.
func TestCrossChunkDanglingThenLand(t *testing.T) {
	m, _ := newTestManager()
	mustCreate(t, m, bulkimport.JobConfig{ID: "j", MaxChunks: 2, Validator: badEntryValidator})

	// Chunk 0 references "b", which lives in the not-yet-arrived chunk 1.
	res := mustSubmit(t, m, chunk("j", 0, entry("a", "b")))
	if res.Outcome != bulkimport.ChunkAccepted {
		t.Fatalf("chunk 0 outcome = %v", res.Outcome)
	}
	if got := res.Entries[0].Status; got != bulkimport.EntryPending {
		t.Fatalf("entry a status after chunk 0 = %v, want pending", got)
	}
	v := mustQuery(t, m, "j", "a")
	if v.Status != bulkimport.EntryPending || len(v.WaitingOn) != 1 ||
		v.WaitingOn[0].TargetID != "b" || v.WaitingOn[0].Reason != bulkimport.WaitTargetNotArrived {
		t.Fatalf("entry a view = %+v, want pending on b/target_not_arrived", v)
	}

	// Chunk 1 declares "b"; "a" must be retried and land automatically.
	res = mustSubmit(t, m, chunk("j", 1, entry("b")))
	if res.Entries[0].Status != bulkimport.EntryLanded {
		t.Fatalf("entry b status = %v, want landed", res.Entries[0].Status)
	}
	if v = mustQuery(t, m, "j", "a"); v.Status != bulkimport.EntryLanded {
		t.Fatalf("entry a view = %+v, want landed", v)
	}
}

// TestRefFailurePropagatesImmediately covers the distinction between
// waiting on a missing chunk and waiting on a failed entry: as soon as
// the referenced entry deterministically fails, the waiter must fail
// too, and the failure must propagate along the reference chain.
func TestRefFailurePropagatesImmediately(t *testing.T) {
	m, _ := newTestManager()
	mustCreate(t, m, bulkimport.JobConfig{ID: "j", MaxChunks: 3, Validator: badEntryValidator})

	// a -> b -> c, all in separate chunks; c will fail validation.
	mustSubmit(t, m, chunk("j", 0, entry("a", "b")))
	mustSubmit(t, m, chunk("j", 1, entry("b", "c")))
	if v := mustQuery(t, m, "j", "b"); v.Status != bulkimport.EntryPending ||
		v.WaitingOn[0].Reason != bulkimport.WaitTargetNotArrived {
		t.Fatalf("entry b view = %+v, want pending on c/target_not_arrived", v)
	}

	res := mustSubmit(t, m, chunk("j", 2, badEntry("c")))
	if res.Entries[0].Status != bulkimport.EntryFailed ||
		res.Entries[0].Err.Kind != bulkimport.ErrKindValidation {
		t.Fatalf("entry c result = %+v, want failed/validation", res.Entries[0])
	}
	// b and a must already be failed by propagation, without any
	// resubmission and without waiting for the chunk limit.
	for _, id := range []string{"b", "a"} {
		v := mustQuery(t, m, "j", id)
		if v.Status != bulkimport.EntryFailed || v.Err.Kind != bulkimport.ErrKindRefFailed {
			t.Fatalf("entry %s view = %+v, want failed/ref_failed", id, v)
		}
	}
	if v := mustQuery(t, m, "j", "b"); v.Err.RefID != "c" {
		t.Fatalf("entry b error ref = %q, want c", v.Err.RefID)
	}
}

// TestMaxChunksTimeout covers the declared chunk-count limit: once all
// chunks arrived, still-dangling entries fail with a timeout verdict
// while landed entries stay untouched.
func TestMaxChunksTimeout(t *testing.T) {
	m, _ := newTestManager()
	mustCreate(t, m, bulkimport.JobConfig{ID: "j", MaxChunks: 2, Validator: badEntryValidator})

	mustSubmit(t, m, chunk("j", 0, entry("a", "ghost"), entry("ok")))
	if v := mustQuery(t, m, "j", "a"); v.Status != bulkimport.EntryPending {
		t.Fatalf("entry a view = %+v, want pending before limit", v)
	}
	mustSubmit(t, m, chunk("j", 1, entry("other")))

	v := mustQuery(t, m, "j", "a")
	if v.Status != bulkimport.EntryTimedOut || v.Err.Kind != bulkimport.ErrKindDanglingTimeout {
		t.Fatalf("entry a view = %+v, want timed_out/dangling_timeout", v)
	}
	for _, id := range []string{"ok", "other"} {
		if v := mustQuery(t, m, "j", id); v.Status != bulkimport.EntryLanded {
			t.Fatalf("entry %s view = %+v, want landed (unaffected by timeout)", id, v)
		}
	}
	// A further distinct chunk exceeds the declared limit.
	res := mustSubmit(t, m, chunk("j", 2, entry("late")))
	if res.Outcome != bulkimport.ChunkRejected || res.Err.Kind != bulkimport.ErrKindChunkLimitExceeded {
		t.Fatalf("chunk 2 result = %+v, want rejected/chunk_limit_exceeded", res)
	}
}

// TestDuplicateSameContent covers idempotent redelivery: an identical
// chunk is a duplicate, its entries are not reprocessed, and the
// verdict does not depend on arrival timing.
func TestDuplicateSameContent(t *testing.T) {
	m, _ := newTestManager()
	sink := &countingSink{fail: map[string]bool{}}
	mustCreate(t, m, bulkimport.JobConfig{ID: "j", MaxChunks: 1, Validator: badEntryValidator, Sink: sink})

	c := chunk("j", 0, entry("a"), entry("b"))
	if res := mustSubmit(t, m, c); res.Outcome != bulkimport.ChunkAccepted {
		t.Fatalf("first submit outcome = %v", res.Outcome)
	}
	res := mustSubmit(t, m, c)
	if res.Outcome != bulkimport.ChunkDuplicate || res.Err != nil {
		t.Fatalf("second submit = %+v, want duplicate without error", res)
	}
	if got := sink.count(); got != 2 {
		t.Fatalf("sink received %d entries, want exactly 2 (no reprocessing)", got)
	}
	stats, err := m.Stats("j")
	if err != nil {
		t.Fatal(err)
	}
	if stats.LandedEntries != 2 || stats.ArrivedChunks != 1 {
		t.Fatalf("stats = %+v, want 2 landed entries and 1 arrived chunk", stats)
	}
}

// TestDuplicateConflictingContent covers a redelivery with the same
// sequence number but different content: it is a distinct error
// category, previously decided results are immutable, and entries that
// only exist in the conflicting chunk are queryable as conflicted.
func TestDuplicateConflictingContent(t *testing.T) {
	m, _ := newTestManager()
	sink := &countingSink{fail: map[string]bool{}}
	mustCreate(t, m, bulkimport.JobConfig{ID: "j", MaxChunks: 5, Validator: badEntryValidator, Sink: sink})

	mustSubmit(t, m, chunk("j", 0, entry("a"), entry("b")))

	conflicting := chunk("j", 0, entry("a"), entry("b"), entry("c"))
	res := mustSubmit(t, m, conflicting)
	if res.Outcome != bulkimport.ChunkRejected || res.Err.Kind != bulkimport.ErrKindChunkConflict {
		t.Fatalf("conflicting resubmit = %+v, want rejected/chunk_conflict", res)
	}
	// Original results are unchanged.
	for _, id := range []string{"a", "b"} {
		if v := mustQuery(t, m, "j", id); v.Status != bulkimport.EntryLanded {
			t.Fatalf("entry %s view = %+v, want landed (immutable)", id, v)
		}
	}
	if got := sink.count(); got != 2 {
		t.Fatalf("sink received %d entries, want 2 (conflict not applied)", got)
	}
	// "c" only ever appeared in the conflicting chunk.
	v := mustQuery(t, m, "j", "c")
	if v.Status != bulkimport.EntryConflicted || v.Err.Kind != bulkimport.ErrKindChunkConflict {
		t.Fatalf("entry c view = %+v, want conflicted/chunk_conflict", v)
	}
	// An identical redelivery after the conflict is still a duplicate.
	if res := mustSubmit(t, m, chunk("j", 0, entry("a"), entry("b"))); res.Outcome != bulkimport.ChunkDuplicate {
		t.Fatalf("identical resubmit after conflict = %+v, want duplicate", res)
	}
}

// TestBidirectionalDangling covers two chunks whose entries reference
// each other: the mutual wait must resolve to a deterministic failure
// instead of dangling forever.
func TestBidirectionalDangling(t *testing.T) {
	m, _ := newTestManager()
	mustCreate(t, m, bulkimport.JobConfig{ID: "j", MaxChunks: 2, Validator: badEntryValidator})

	mustSubmit(t, m, chunk("j", 0, entry("a", "b")))
	mustSubmit(t, m, chunk("j", 1, entry("b", "a")))

	for _, id := range []string{"a", "b"} {
		v := mustQuery(t, m, "j", id)
		if v.Status != bulkimport.EntryFailed || v.Err.Kind != bulkimport.ErrKindValidation {
			t.Fatalf("entry %s view = %+v, want failed/validation (circular)", id, v)
		}
	}
	stats, err := m.Stats("j")
	if err != nil {
		t.Fatal(err)
	}
	if stats.PendingEntries != 0 || stats.DanglingRefs != 0 {
		t.Fatalf("stats = %+v, want no dangling state left", stats)
	}
}

// TestBidirectionalDanglingConcurrent is the concurrent variant: the
// two chunks race each other and must still resolve deterministically.
func TestBidirectionalDanglingConcurrent(t *testing.T) {
	for i := 0; i < 50; i++ {
		m, _ := newTestManager()
		mustCreate(t, m, bulkimport.JobConfig{ID: "j", MaxChunks: 2, Validator: badEntryValidator})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); mustSubmit(t, m, chunk("j", 0, entry("a", "b"))) }()
		go func() { defer wg.Done(); mustSubmit(t, m, chunk("j", 1, entry("b", "a"))) }()
		wg.Wait()
		for _, id := range []string{"a", "b"} {
			v := mustQuery(t, m, "j", id)
			if v.Status != bulkimport.EntryFailed || v.Err.Kind != bulkimport.ErrKindValidation {
				t.Fatalf("iter %d: entry %s view = %+v, want failed/validation (circular)", i, id, v)
			}
		}
	}
}

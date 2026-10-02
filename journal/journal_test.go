package journal

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func mustWrite(t *testing.T, j *Journal, block int, payload string) {
	t.Helper()
	if err := j.Write(block, []byte(payload)); err != nil {
		t.Fatalf("Write(%d, %q): %v", block, payload, err)
	}
}

func mustRevoke(t *testing.T, j *Journal, block int) {
	t.Helper()
	if err := j.Revoke(block); err != nil {
		t.Fatalf("Revoke(%d): %v", block, err)
	}
}

func mustCommit(t *testing.T, j *Journal) {
	t.Helper()
	if err := j.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
}

func mustCheckpoint(t *testing.T, j *Journal, upto int) {
	t.Helper()
	if err := j.Checkpoint(upto); err != nil {
		t.Fatalf("Checkpoint(%d): %v", upto, err)
	}
}

func mustRecover(t *testing.T, j *Journal, disk map[int]Block, n int) Result {
	t.Helper()
	res, err := j.Recover(disk, n)
	if err != nil {
		t.Fatalf("Recover(n=%d): %v", n, err)
	}
	return res
}

func block(ver int, payload string) Block {
	return Block{Ver: ver, Payload: []byte(payload)}
}

func checkImage(t *testing.T, res Result, want map[int]Block) {
	t.Helper()
	if !reflect.DeepEqual(res.Image, want) {
		t.Fatalf("image = %v, want %v", res.Image, want)
	}
}

func checkCounters(t *testing.T, res Result, applied, skipped, stale, checkpointed, ignored int) {
	t.Helper()
	if res.Applied != applied || res.Skipped != skipped || res.Stale != stale ||
		res.Checkpointed != checkpointed || res.Ignored != ignored {
		t.Fatalf("counters = (applied %d, skipped %d, stale %d, checkpointed %d, ignored %d), want (%d, %d, %d, %d, %d)",
			res.Applied, res.Skipped, res.Stale, res.Checkpointed, res.Ignored,
			applied, skipped, stale, checkpointed, ignored)
	}
}

// TestSpecExample replays the worked example from the specification.
func TestSpecExample(t *testing.T) {
	j, err := New(4)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mustWrite(t, j, 5, "a") // record 1
	mustCommit(t, j)        // record 2
	mustRevoke(t, j, 5)     // record 3
	mustCommit(t, j)        // record 4
	mustWrite(t, j, 5, "c") // record 5
	mustWrite(t, j, 7, "d") // record 6
	mustCommit(t, j)        // record 7
	if got := j.Records(); got != 7 {
		t.Fatalf("Records = %d, want 7", got)
	}
	disk := map[int]Block{7: block(3, "old")}

	res := mustRecover(t, j, disk, 7)
	checkCounters(t, res, 1, 1, 1, 0, 0)
	checkImage(t, res, map[int]Block{5: block(3, "c")})

	mustCheckpoint(t, j, 1) // record 8
	res = mustRecover(t, j, disk, 8)
	checkCounters(t, res, 1, 0, 1, 1, 0)
	checkImage(t, res, map[int]Block{5: block(3, "c")})
	if res.Applied+res.Skipped+res.Stale+res.Checkpointed != 3 {
		t.Fatalf("counter sum = %d, want 3 committed writes",
			res.Applied+res.Skipped+res.Stale+res.Checkpointed)
	}
}

// TestWriteThenRevokeSameTxn: a Write followed by a Revoke of the same
// block inside one transaction is skipped by its own revoke entry.
func TestWriteThenRevokeSameTxn(t *testing.T) {
	j, _ := New(4)
	mustWrite(t, j, 1, "x")
	mustRevoke(t, j, 1)
	mustCommit(t, j)

	res := mustRecover(t, j, nil, j.Records())
	checkCounters(t, res, 0, 1, 0, 0, 0)
	checkImage(t, res, map[int]Block{})
}

// TestRevokeThenWriteSameTxn: a Revoke followed by a Write of the same
// block makes the revoke ineffective; an earlier transaction's write to
// the block is applied as usual.
func TestRevokeThenWriteSameTxn(t *testing.T) {
	j, _ := New(4)
	mustWrite(t, j, 1, "a")
	mustCommit(t, j)
	mustRevoke(t, j, 1)
	mustWrite(t, j, 1, "b")
	mustCommit(t, j)

	res := mustRecover(t, j, nil, j.Records())
	checkCounters(t, res, 2, 0, 0, 0, 0)
	checkImage(t, res, map[int]Block{1: block(2, "b")})
}

// TestRevokeSkipAndRewrite: an earlier transaction's write is skipped
// because a later transaction revokes the block, and a still later
// transaction writes it again.
func TestRevokeSkipAndRewrite(t *testing.T) {
	j, _ := New(4)
	mustWrite(t, j, 1, "a")
	mustCommit(t, j)
	mustRevoke(t, j, 1)
	mustCommit(t, j)
	mustWrite(t, j, 1, "c")
	mustCommit(t, j)

	res := mustRecover(t, j, nil, j.Records())
	checkCounters(t, res, 1, 1, 0, 0, 0)
	checkImage(t, res, map[int]Block{1: block(3, "c")})
}

// TestRevokeBoundary: a write whose tid equals the revoke-table value is
// skipped; tid one greater is written.
func TestRevokeBoundary(t *testing.T) {
	j, _ := New(4)
	mustWrite(t, j, 1, "a") // txn 1
	mustCommit(t, j)
	mustWrite(t, j, 1, "x") // txn 2: write then revoke, revoke wins
	mustRevoke(t, j, 1)
	mustCommit(t, j)
	mustWrite(t, j, 1, "c") // txn 3: tid 3 > revoke value 2
	mustCommit(t, j)

	res := mustRecover(t, j, nil, j.Records())
	// t1 skipped (2 >= 1), t2 skipped (2 >= 2), t3 applied (2 < 3).
	checkCounters(t, res, 1, 2, 0, 0, 0)
	checkImage(t, res, map[int]Block{1: block(3, "c")})
}

// TestDiskVersionBoundary: a write is Stale when the disk version equals
// its tid, and applied when the disk version is one less.
func TestDiskVersionBoundary(t *testing.T) {
	j, _ := New(4)
	mustWrite(t, j, 1, "w1") // txn 1: disk ver 2 >= 1, stale
	mustCommit(t, j)
	mustWrite(t, j, 1, "w2") // txn 2: disk ver 2 >= 2, stale
	mustCommit(t, j)
	mustWrite(t, j, 1, "w3") // txn 3: disk ver 2 < 3, applied
	mustCommit(t, j)

	disk := map[int]Block{1: block(2, "old")}
	res := mustRecover(t, j, disk, j.Records())
	checkCounters(t, res, 1, 0, 2, 0, 0)
	checkImage(t, res, map[int]Block{1: block(3, "w3")})
	// disk must not be modified.
	if !reflect.DeepEqual(disk, map[int]Block{1: block(2, "old")}) {
		t.Fatalf("disk mutated: %v", disk)
	}
}

// TestTwoWritesSameBlockSameTxn: two writes of one block in a single
// transaction are both applied; the second is not skipped even though
// the first already raised the image version to t.
func TestTwoWritesSameBlockSameTxn(t *testing.T) {
	j, _ := New(4)
	mustWrite(t, j, 1, "a")
	mustWrite(t, j, 1, "b")
	mustCommit(t, j)

	res := mustRecover(t, j, nil, j.Records())
	checkCounters(t, res, 2, 0, 0, 0, 0)
	checkImage(t, res, map[int]Block{1: block(1, "b")})
}

// TestCheckpointBoundary: a write whose tid equals the checkpoint
// watermark K is Checkpointed; tid K+1 continues to the other checks.
func TestCheckpointBoundary(t *testing.T) {
	j, _ := New(4)
	mustWrite(t, j, 1, "a") // txn 1: tid == K, checkpointed
	mustCommit(t, j)
	mustWrite(t, j, 2, "b") // txn 2: tid == K+1, applied
	mustCommit(t, j)
	mustCheckpoint(t, j, 1)

	res := mustRecover(t, j, nil, j.Records())
	checkCounters(t, res, 1, 0, 0, 1, 0)
	checkImage(t, res, map[int]Block{2: block(2, "b")})
}

// TestCheckpointBeforeRevoke: the checkpoint check runs before the
// revoke check, so a checkpointed write is not counted as Skipped even
// when the block is revoked by a later transaction.
func TestCheckpointBeforeRevoke(t *testing.T) {
	j, _ := New(4)
	mustWrite(t, j, 1, "a")
	mustCommit(t, j)
	mustRevoke(t, j, 1)
	mustCommit(t, j)
	mustCheckpoint(t, j, 1)

	res := mustRecover(t, j, nil, j.Records())
	checkCounters(t, res, 0, 0, 0, 1, 0)
	checkImage(t, res, map[int]Block{})
}

// TestEmptyPayloadVsNeverWritten: an empty-payload write produces an
// image entry with an empty payload, unlike a block that was never
// written, which is absent from the image.
func TestEmptyPayloadVsNeverWritten(t *testing.T) {
	j, _ := New(4)
	if err := j.Write(1, []byte{}); err != nil {
		t.Fatalf("Write empty payload: %v", err)
	}
	if err := j.Write(2, nil); err != nil {
		t.Fatalf("Write nil payload: %v", err)
	}
	mustCommit(t, j)

	res := mustRecover(t, j, nil, j.Records())
	checkCounters(t, res, 2, 0, 0, 0, 0)
	b1, ok := res.Image[1]
	if !ok {
		t.Fatalf("block 1 missing from image %v", res.Image)
	}
	if b1.Ver != 1 || b1.Payload == nil || len(b1.Payload) != 0 {
		t.Fatalf("block 1 = %+v, want ver 1 with empty non-nil payload", b1)
	}
	if _, ok := res.Image[2]; !ok {
		t.Fatalf("block 2 missing from image %v", res.Image)
	}
	if _, ok := res.Image[3]; ok {
		t.Fatalf("never-written block 3 present in image %v", res.Image)
	}
}

// committedWritesInPrefix counts Write records of transactions whose
// Commit record lies within the first n records.
func committedWritesInPrefix(j *Journal, n int) int {
	j.mu.Lock()
	defer j.mu.Unlock()
	committed := map[int]bool{}
	for _, r := range j.records[:n] {
		if r.Kind == CommitRecord {
			committed[r.Tid] = true
		}
	}
	total := 0
	for _, r := range j.records[:n] {
		if r.Kind == WriteRecord && committed[r.Tid] {
			total++
		}
	}
	return total
}

// ignoredInPrefix counts Write/Revoke records of transactions without a
// Commit record within the first n records.
func ignoredInPrefix(j *Journal, n int) int {
	j.mu.Lock()
	defer j.mu.Unlock()
	committed := map[int]bool{}
	for _, r := range j.records[:n] {
		if r.Kind == CommitRecord {
			committed[r.Tid] = true
		}
	}
	total := 0
	for _, r := range j.records[:n] {
		if (r.Kind == WriteRecord || r.Kind == RevokeRecord) && !committed[r.Tid] {
			total++
		}
	}
	return total
}

// buildPrefixLog builds a log with checkpoints interleaved between the
// records of open transactions and a trailing uncommitted transaction.
func buildPrefixLog(t *testing.T) *Journal {
	t.Helper()
	j, err := New(4)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mustWrite(t, j, 1, "a") // record 1, txn 1
	mustCheckpoint(t, j, 0) // record 2
	mustCommit(t, j)        // record 3
	mustRevoke(t, j, 1)     // record 4, txn 2
	mustCheckpoint(t, j, 1) // record 5, mid-transaction
	mustCommit(t, j)        // record 6
	mustWrite(t, j, 1, "c") // record 7, txn 3
	mustWrite(t, j, 2, "d") // record 8
	mustCommit(t, j)        // record 9
	mustCheckpoint(t, j, 3) // record 10
	mustWrite(t, j, 3, "e") // record 11, txn 4 stays open
	return j
}

// TestAllPrefixes walks every prefix length n, including prefixes that
// cut just before and just after Commit and Checkpoint records, and
// checks the counter-sum invariant plus hand-computed values at key
// prefixes.
func TestAllPrefixes(t *testing.T) {
	j := buildPrefixLog(t)
	disk := map[int]Block{2: block(3, "old")}
	total := j.Records()
	if total != 11 {
		t.Fatalf("Records = %d, want 11", total)
	}

	type want struct {
		applied, skipped, stale, checkpointed, ignored int
	}
	key := map[int]want{
		0:  {0, 0, 0, 0, 0},
		1:  {0, 0, 0, 0, 1}, // txn 1 write, commit not in prefix
		2:  {0, 0, 0, 0, 1}, // checkpoint(0) changes nothing
		3:  {1, 0, 0, 0, 0}, // txn 1 committed, K=0, applied
		4:  {1, 0, 0, 0, 1}, // txn 2 revoke uncommitted: ignored
		5:  {0, 0, 0, 1, 1}, // K=1: txn 1 write checkpointed
		6:  {0, 0, 0, 1, 0}, // txn 2 committed, revoke table b1->2
		7:  {0, 0, 0, 1, 1}, // txn 3 write not yet committed
		9:  {1, 0, 1, 1, 0}, // txn 3: b1 applied (2 < 3), b2 stale (3 >= 3)
		10: {0, 0, 0, 3, 0}, // K=3: all committed writes checkpointed
		11: {0, 0, 0, 3, 1}, // txn 4 write uncommitted: ignored
	}
	for n := 0; n <= total; n++ {
		res := mustRecover(t, j, disk, n)
		sum := res.Applied + res.Skipped + res.Stale + res.Checkpointed
		if wantWrites := committedWritesInPrefix(j, n); sum != wantWrites {
			t.Fatalf("n=%d: counter sum %d, want %d committed writes", n, sum, wantWrites)
		}
		if wantIgnored := ignoredInPrefix(j, n); res.Ignored != wantIgnored {
			t.Fatalf("n=%d: Ignored = %d, want %d", n, res.Ignored, wantIgnored)
		}
		if w, ok := key[n]; ok {
			checkCounters(t, res, w.applied, w.skipped, w.stale, w.checkpointed, w.ignored)
		}
	}

	// Appending more records must not change the replay of prefix 9.
	before := mustRecover(t, j, disk, 9)
	mustCommit(t, j)
	mustWrite(t, j, 1, "f")
	mustCheckpoint(t, j, 4)
	after := mustRecover(t, j, disk, 9)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("prefix 9 replay changed after append:\nbefore %+v\nafter  %+v", before, after)
	}
}

// TestUncommittedRevokeIgnored: a Revoke in an uncommitted transaction
// has no revoke effect and is counted in Ignored.
func TestUncommittedRevokeIgnored(t *testing.T) {
	j, _ := New(4)
	mustWrite(t, j, 1, "a")
	mustCommit(t, j)
	mustRevoke(t, j, 1) // never committed

	res := mustRecover(t, j, nil, j.Records())
	checkCounters(t, res, 1, 0, 0, 0, 1)
	checkImage(t, res, map[int]Block{1: block(1, "a")})
}

// TestRejections checks every rejection reason via errors.Is, the
// rejection precedence order, and that rejected calls leave the log and
// the open transaction untouched.
func TestRejections(t *testing.T) {
	if _, err := New(0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("New(0) = %v, want ErrInvalidArgument", err)
	}
	if _, err := New(-3); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("New(-3) = %v, want ErrInvalidArgument", err)
	}

	j, err := New(2)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	assertState := func(records int) {
		t.Helper()
		if got := j.Records(); got != records {
			t.Fatalf("Records = %d, want %d after rejected ops", got, records)
		}
	}

	// Invalid arguments.
	if err := j.Write(-1, []byte("x")); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Write(-1) = %v", err)
	}
	if err := j.Write(0, make([]byte, MaxPayload+1)); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Write oversize = %v", err)
	}
	if err := j.Revoke(-2); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Revoke(-2) = %v", err)
	}
	if err := j.Checkpoint(1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Checkpoint(1) with 0 committed = %v", err)
	}
	if err := j.Checkpoint(-1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Checkpoint(-1) = %v", err)
	}
	if _, err := j.Recover(nil, -1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Recover(-1) = %v", err)
	}
	if _, err := j.Recover(nil, 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Recover(1) past end = %v", err)
	}
	// Empty transaction.
	if err := j.Commit(); !errors.Is(err, ErrEmptyTransaction) {
		t.Fatalf("Commit empty = %v", err)
	}
	assertState(0)

	// Fill the transaction to Cap.
	mustWrite(t, j, 1, "a")
	mustRevoke(t, j, 2)
	// Invalid argument wins over a full transaction.
	if err := j.Write(-1, nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Write(-1) on full txn = %v, want ErrInvalidArgument", err)
	}
	if err := j.Write(3, []byte("x")); !errors.Is(err, ErrTransactionFull) {
		t.Fatalf("Write on full txn = %v", err)
	}
	if err := j.Revoke(3); !errors.Is(err, ErrTransactionFull) {
		t.Fatalf("Revoke on full txn = %v", err)
	}
	assertState(2)

	// Commit and Checkpoint do not count towards Cap.
	mustCommit(t, j)
	mustCheckpoint(t, j, 1)
	mustCheckpoint(t, j, 1) // equal to previous is allowed
	if err := j.Checkpoint(0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Checkpoint(0) below last = %v", err)
	}
	if err := j.Checkpoint(2); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Checkpoint(2) above committed = %v", err)
	}
	assertState(5)

	// The log still works normally afterwards.
	if err := j.Write(9, make([]byte, MaxPayload)); err != nil {
		t.Fatalf("Write max payload: %v", err)
	}
	mustCommit(t, j)
	res := mustRecover(t, j, nil, j.Records())
	if res.Applied != 1 || res.Checkpointed != 1 {
		t.Fatalf("unexpected counters %+v", res)
	}
	if got := len(res.Image[9].Payload); got != MaxPayload {
		t.Fatalf("payload len = %d, want %d", got, MaxPayload)
	}
}

// TestPayloadCopiedOnAppend: mutating the caller's buffer after Write
// must not affect the log, and mutating a recovered image must not
// affect later Recover calls.
func TestPayloadCopiedOnAppend(t *testing.T) {
	j2, _ := New(4)
	raw := []byte("abc")
	if err := j2.Write(1, raw); err != nil {
		t.Fatalf("Write: %v", err)
	}
	raw[0] = 'Z'
	mustCommit(t, j2)

	res := mustRecover(t, j2, nil, j2.Records())
	if got := string(res.Image[1].Payload); got != "abc" {
		t.Fatalf("payload = %q, want %q (append must copy)", got, "abc")
	}
	res.Image[1].Payload[0] = 'Y'
	again := mustRecover(t, j2, nil, j2.Records())
	if got := string(again.Image[1].Payload); got != "abc" {
		t.Fatalf("payload = %q after image mutation, want %q", got, "abc")
	}
}

// TestDeterministicReplay: the same operation sequence builds exactly
// the same log, and every prefix replays identically.
func TestDeterministicReplay(t *testing.T) {
	build := func() *Journal {
		j, _ := New(3)
		mustWrite(t, j, 1, "a")
		mustRevoke(t, j, 2)
		mustCommit(t, j)
		mustCheckpoint(t, j, 1)
		mustWrite(t, j, 2, "b")
		mustWrite(t, j, 1, "c")
		mustCommit(t, j)
		return j
	}
	j1, j2 := build(), build()
	if j1.Records() != j2.Records() {
		t.Fatalf("Records differ: %d vs %d", j1.Records(), j2.Records())
	}
	disk := map[int]Block{1: block(0, "d")}
	for n := 0; n <= j1.Records(); n++ {
		r1 := mustRecover(t, j1, disk, n)
		r2 := mustRecover(t, j2, disk, n)
		if !reflect.DeepEqual(r1, r2) {
			t.Fatalf("n=%d: replay differs:\n%+v\n%+v", n, r1, r2)
		}
	}
}

// TestConcurrentUse hammers the journal from many goroutines; with the
// race detector enabled this validates the serializability claim, and
// the final replay must still satisfy the counter invariant.
func TestConcurrentUse(t *testing.T) {
	j, _ := New(3)
	disk := map[int]Block{0: block(1, "old")}
	var wg sync.WaitGroup

	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				b := (g + i) % 5
				var err error
				if i%2 == 0 {
					err = j.Write(b, []byte(fmt.Sprintf("g%d-i%d", g, i)))
				} else {
					err = j.Revoke(b)
				}
				if errors.Is(err, ErrTransactionFull) {
					_ = j.Commit()
				}
				if i%7 == 0 {
					_ = j.Commit() // may be ErrEmptyTransaction; harmless
				}
			}
		}(g)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_ = j.Checkpoint(0) // only valid until the first real checkpoint
		}
	}()
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				n := j.Records()
				if _, err := j.Recover(disk, n); err != nil {
					t.Errorf("Recover(%d): %v", n, err)
					return
				}
			}
		}()
	}
	wg.Wait()

	res := mustRecover(t, j, disk, j.Records())
	if sum := res.Applied + res.Skipped + res.Stale + res.Checkpointed; sum != committedWritesInPrefix(j, j.Records()) {
		t.Fatalf("counter sum %d != committed writes %d", sum, committedWritesInPrefix(j, j.Records()))
	}
	if res.Ignored != ignoredInPrefix(j, j.Records()) {
		t.Fatalf("Ignored = %d, want %d", res.Ignored, ignoredInPrefix(j, j.Records()))
	}
}

// TestRecordString keeps fmt/strings in use for log formatting helpers
// shared with the randomized tests.
func TestRecordString(t *testing.T) {
	r := Record{Kind: WriteRecord, Tid: 2, Block: 3, Payload: []byte("xy")}
	if got := formatRecord(r); !strings.Contains(got, "Write") || !strings.Contains(got, "tid=2") {
		t.Fatalf("formatRecord = %q", got)
	}
}

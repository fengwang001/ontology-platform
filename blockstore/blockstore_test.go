package blockstore

import (
	"errors"
	"testing"
)

func mustBegin(t *testing.T, st *Store, id string) {
	t.Helper()
	if err := st.BeginSession(id); err != nil {
		t.Fatalf("BeginSession(%q): %v", id, err)
	}
}

func mustUpload(t *testing.T, st *Store, sess string, data []byte) string {
	t.Helper()
	d, err := st.Upload(sess, data)
	if err != nil {
		t.Fatalf("Upload(%q): %v", sess, err)
	}
	return d
}

func mustCommit(t *testing.T, st *Store, sess, snap string, refs []string) {
	t.Helper()
	if err := st.Commit(sess, snap, refs); err != nil {
		t.Fatalf("Commit(%q/%q): %v", sess, snap, err)
	}
}

// Upload of identical content deduplicates to the same digest regardless of
// which session uploads it.
func TestDeduplication(t *testing.T) {
	st := New(Config{})
	mustBegin(t, st, "a")
	mustBegin(t, st, "b")

	d1 := mustUpload(t, st, "a", []byte("payload"))
	d2 := mustUpload(t, st, "b", []byte("payload"))
	if d1 != d2 {
		t.Fatalf("expected identical digest, got %q vs %q", d1, d2)
	}
	if got := st.StateOf(d1); got != Normal {
		t.Fatalf("block state = %s, want normal", got)
	}
}

// All blocks referenced by a committed manifest always read back fully.
func TestCommittedSnapshotReadsBack(t *testing.T) {
	st := New(Config{})
	mustBegin(t, st, "s")
	d1 := mustUpload(t, st, "s", []byte("one"))
	d2 := mustUpload(t, st, "s", []byte("two"))
	mustCommit(t, st, "s", "snap", []string{d1, d2})

	for _, tc := range [][2]string{{d1, "one"}, {d2, "two"}} {
		data, err := st.Read(tc[0])
		if err != nil || string(data) != tc[1] {
			t.Fatalf("Read(%q) = %q, %v; want %q", tc[0], data, err, tc[1])
		}
	}
}

// Required: session started before Mark, commits only after Mark and
// references a block Mark classified as pending -> the block survives.
func TestMarkThenLateCommitRevivesPending(t *testing.T) {
	st := New(Config{})

	mustBegin(t, st, "old-writer")
	oldD := mustUpload(t, st, "old-writer", []byte("old"))
	garbageD := mustUpload(t, st, "old-writer", []byte("garbage"))
	mustCommit(t, st, "old-writer", "old-snap", []string{oldD})
	st.End("old-writer")

	// Long session starts BEFORE Mark.
	mustBegin(t, st, "late")

	if marked := st.Mark(); marked != 1 {
		t.Fatalf("Mark marked %d blocks, want 1", marked)
	}
	if got := st.StateOf(garbageD); got != Pending {
		t.Fatalf("garbage state = %s, want pending", got)
	}
	// Pending blocks are not deleted and remain readable.
	if data, err := st.Read(garbageD); err != nil || string(data) != "garbage" {
		t.Fatalf("pending block not readable: %q, %v", data, err)
	}

	mustCommit(t, st, "late", "late-snap", []string{oldD, garbageD})

	deleted, err := st.Sweep()
	if err != nil || deleted != 0 {
		t.Fatalf("Sweep deleted %d blocks (err %v), want 0", deleted, err)
	}
	if got := st.StateOf(garbageD); got != Normal {
		t.Fatalf("revived block state = %s, want normal", got)
	}
	if _, err := st.Read(garbageD); err != nil {
		t.Fatalf("revived block unreadable: %v", err)
	}
}

// Session uploads (reuses) a pending block: restored to normal immediately.
func TestReuseOfPendingRestoresNormal(t *testing.T) {
	st := New(Config{})

	mustBegin(t, st, "first")
	pendingD := mustUpload(t, st, "first", []byte("lonely"))
	st.End("first")

	if marked := st.Mark(); marked != 1 {
		t.Fatalf("Mark = %d, want 1", marked)
	}

	mustBegin(t, st, "second")
	d := mustUpload(t, st, "second", []byte("lonely"))
	if d != pendingD {
		t.Fatalf("reuse digest mismatch: %q vs %q", d, pendingD)
	}
	if got := st.StateOf(pendingD); got != Normal {
		t.Fatalf("after reuse state = %s, want normal", got)
	}
	mustCommit(t, st, "second", "saved", []string{pendingD})

	if deleted, err := st.Sweep(); err != nil || deleted != 0 {
		t.Fatalf("Sweep = %d, %v; want 0 deletions", deleted, err)
	}
}

// A session open at Mark time blocks Sweep until it ends.
func TestLongSessionBlocksSweep(t *testing.T) {
	st := New(Config{})

	mustBegin(t, st, "short")
	garbageD := mustUpload(t, st, "short", []byte("garbage"))
	st.End("short")

	mustBegin(t, st, "long") // open at Mark time
	if marked := st.Mark(); marked != 1 {
		t.Fatalf("Mark = %d, want 1", marked)
	}

	for i := 0; i < 3; i++ {
		deleted, err := st.Sweep()
		if err != nil {
			t.Fatalf("Sweep %d: %v", i, err)
		}
		if deleted != 0 {
			t.Fatalf("Sweep %d deleted %d blocks while registrant open", i, deleted)
		}
		if got := st.StateOf(garbageD); got != Pending {
			t.Fatalf("blocked sweep changed state to %s, want pending", got)
		}
	}

	// After the registrant ends, the next cycle deletes the block.
	st.End("long")
	if deleted, err := st.Sweep(); err != nil || deleted != 1 {
		t.Fatalf("Sweep after End = %d, %v; want 1 deletion", deleted, err)
	}
	if got := st.StateOf(garbageD); got != Deleted {
		t.Fatalf("garbage state = %s, want deleted", got)
	}
	if _, err := st.Read(garbageD); !errors.Is(err, ErrBlockDeleted) {
		t.Fatalf("Read deleted block err = %v, want ErrBlockDeleted", err)
	}
}

// A block nobody references is deleted after one complete Mark/Sweep cycle.
func TestUnreferencedBlockDeletedAfterTwoPhases(t *testing.T) {
	st := New(Config{})
	mustBegin(t, st, "s")
	keepD := mustUpload(t, st, "s", []byte("keep"))
	dropD := mustUpload(t, st, "s", []byte("drop"))
	mustCommit(t, st, "s", "snap", []string{keepD})

	if marked := st.Mark(); marked != 1 {
		t.Fatalf("Mark = %d, want 1 (only drop)", marked)
	}
	if got := st.StateOf(dropD); got != Pending {
		t.Fatalf("drop state = %s, want pending", got)
	}
	if got := st.StateOf(keepD); got != Normal {
		t.Fatalf("keep state = %s, want normal", got)
	}
	if deleted, err := st.Sweep(); err != nil || deleted != 1 {
		t.Fatalf("Sweep = %d, %v; want 1", deleted, err)
	}
	if _, err := st.Read(dropD); !errors.Is(err, ErrBlockDeleted) {
		t.Fatalf("Read drop err = %v, want ErrBlockDeleted", err)
	}
	if _, err := st.Read(keepD); err != nil {
		t.Fatalf("referenced block lost: %v", err)
	}
}

func TestSweepWithoutMark(t *testing.T) {
	st := New(Config{})
	if _, err := st.Sweep(); !errors.Is(err, ErrNoGCCycle) {
		t.Fatalf("Sweep without Mark err = %v, want ErrNoGCCycle", err)
	}
}

// Rejected commit: manifest references a never-uploaded, non-existent block.
// No manifest is left and no block state changes.
func TestCommitMissingBlockIsAtomic(t *testing.T) {
	st := New(Config{})
	mustBegin(t, st, "s")
	d := mustUpload(t, st, "s", []byte("real"))
	fake := Digest([]byte("never uploaded"))

	err := st.Commit("s", "snap", []string{d, fake})
	if !errors.Is(err, ErrBlockMissing) {
		t.Fatalf("Commit err = %v, want ErrBlockMissing", err)
	}
	if ids := st.SnapshotIDs(); len(ids) != 0 {
		t.Fatalf("snapshots after rejected commit = %v, want none", ids)
	}
	if got := st.StateOf(d); got != Normal {
		t.Fatalf("existing block state = %s, want normal", got)
	}
	if got := st.StateOf(fake); got != Deleted {
		t.Fatalf("missing block state = %s, want deleted", got)
	}
	// Session remains usable and commits a valid manifest afterwards.
	mustCommit(t, st, "s", "snap", []string{d})
}

func TestCommitSessionErrors(t *testing.T) {
	st := New(Config{})

	if err := st.Commit("ghost", "x", nil); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("ghost commit err = %v, want ErrSessionNotFound", err)
	}

	mustBegin(t, st, "s")
	mustCommit(t, st, "s", "first", nil)
	if err := st.Commit("s", "second", nil); !errors.Is(err, ErrDuplicateCommit) {
		t.Fatalf("duplicate commit err = %v, want ErrDuplicateCommit", err)
	}
	if ids := st.SnapshotIDs(); len(ids) != 1 || ids[0] != "first" {
		t.Fatalf("snapshots = %v, want only [first]", ids)
	}

	mustBegin(t, st, "t")
	st.End("t")
	if err := st.Commit("t", "x", nil); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("ended session commit err = %v, want ErrSessionNotFound", err)
	}

	if _, err := st.Upload("nope", []byte("x")); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("upload to unknown session err = %v", err)
	}
	if _, err := st.Upload("s", []byte("x")); !errors.Is(err, ErrDuplicateCommit) {
		t.Fatalf("upload to committed session err = %v", err)
	}
}

// Capacity is enforced; failed uploads change neither used bytes nor state.
func TestCapacityFull(t *testing.T) {
	st := New(Config{Capacity: 5})
	mustBegin(t, st, "s")

	if _, err := st.Upload("s", make([]byte, 6)); !errors.Is(err, ErrCapacityFull) {
		t.Fatalf("oversize upload err = %v, want ErrCapacityFull", err)
	}
	d := mustUpload(t, st, "s", make([]byte, 4))
	if _, err := st.Upload("s", make([]byte, 2)); !errors.Is(err, ErrCapacityFull) {
		t.Fatalf("over-capacity upload err = %v, want ErrCapacityFull", err)
	}
	if got := st.StateOf(d); got != Normal {
		t.Fatalf("stored block state = %s, want normal", got)
	}
	if st.used != 4 {
		t.Fatalf("used = %d, want 4", st.used)
	}
}

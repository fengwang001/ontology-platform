package kvlog

import "testing"

// sealedIDs returns all sealed segment ids currently on disk.
func sealedIDs(t *testing.T, dir string) []int {
	t.Helper()
	states, err := listSegments(dir)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int
	for _, st := range states {
		if st.sealed {
			ids = append(ids, st.id)
		}
	}
	return ids
}

func expectGet(t *testing.T, e *Engine, key string, st modelState, val string) {
	t.Helper()
	r, err := e.Get([]byte(key))
	if err != nil {
		t.Fatalf("get %s: %v", key, err)
	}
	switch st {
	case modelPresent:
		if r.Status != StatusPresent || string(r.Value) != val {
			t.Fatalf("get %s = (%d,%q)", key, r.Status, r.Value)
		}
	case modelDeleted:
		if r.Status != StatusDeleted {
			t.Fatalf("get %s status=%d want deleted", key, r.Status)
		}
	case modelMissing:
		if r.Status != StatusMissing {
			t.Fatalf("get %s status=%d want missing", key, r.Status)
		}
	}
}

// TestMergeTombstoneDropAndKeep covers both tombstone conditions:
//   - kdrop appears only in the merged set ending in a tombstone, with no
//     older record outside: the tombstone may be discarded.
//   - kkeep has an older value inside the merged set's tombstone AND an
//     older record outside the merged set: the tombstone must be retained
//     so the old value cannot resurrect.
func TestMergeTombstoneDropAndKeep(t *testing.T) {
	dir := t.TempDir()
	// Layout with max=30 (one record per segment):
	//   seg1 kdrop=d1, seg2 kkeep=keep-old, seg3 kdrop del,
	//   seg4 kkeep del, active seg5 active=x.
	e, err := Open(dir, Config{MaxSegmentBytes: 30, WriteHints: true})
	if err != nil {
		t.Fatal(err)
	}
	must := func(seq uint64, err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(e.Put([]byte("kdrop"), []byte("d1")))
	must(e.Put([]byte("kkeep"), []byte("keep-old")))
	must(e.Delete([]byte("kdrop")))
	must(e.Delete([]byte("kkeep")))
	must(e.Put([]byte("active"), []byte("x")))
	ids := sealedIDs(t, dir)
	if len(ids) != 4 {
		t.Fatalf("sealed=%v", ids)
	}

	// Merge the non-contiguous tombstone segments {3,4}: seg1/seg2
	// survive with older records, so both tombstones must be retained.
	out, err := e.Merge([]int{ids[2], ids[3]})
	if err != nil {
		t.Fatal(err)
	}
	expectGet(t, e, "kdrop", modelDeleted, "")
	expectGet(t, e, "kkeep", modelDeleted, "")
	expectGet(t, e, "active", modelPresent, "x")

	// Merge {1,2,out}: no surviving segment holds either key, so both
	// tombstones may be discarded and the keys become missing.
	out2, err := e.Merge([]int{ids[0], ids[1], out})
	if err != nil {
		t.Fatal(err)
	}
	expectGet(t, e, "kdrop", modelMissing, "")
	expectGet(t, e, "kkeep", modelMissing, "")
	expectGet(t, e, "active", modelPresent, "x")
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}

	e2, err := Open(dir, Config{MaxSegmentBytes: 30, WriteHints: true})
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close()
	expectGet(t, e2, "kdrop", modelMissing, "")
	expectGet(t, e2, "kkeep", modelMissing, "")
	expectGet(t, e2, "active", modelPresent, "x")
	ids2 := sealedIDs(t, dir)
	if len(ids2) != 1 || ids2[0] != out2 {
		t.Fatalf("sealed after merge chain=%v want [%d]", ids2, out2)
	}
}

// TestMergeNonContiguous merges a non-contiguous set and checks exact
// equivalence with a pre-merge snapshot plus post-merge reopening.
func TestMergeNonContiguous(t *testing.T) {
	dir := t.TempDir()
	e, err := Open(dir, Config{MaxSegmentBytes: 30, WriteHints: true})
	if err != nil {
		t.Fatal(err)
	}
	model := newNaiveModel()
	op := func(f func() (uint64, error), k, v string, del bool) {
		if _, err := f(); err != nil {
			t.Fatal(err)
		}
		if del {
			model.del(k)
		} else {
			model.put(k, v)
		}
	}
	op(func() (uint64, error) { return e.Put([]byte("a"), []byte("1")) }, "a", "1", false)
	op(func() (uint64, error) { return e.Put([]byte("b"), []byte("22")) }, "b", "22", false)
	op(func() (uint64, error) { return e.Put([]byte("a"), []byte("11")) }, "a", "11", false)
	op(func() (uint64, error) { return e.Put([]byte("c"), []byte("333")) }, "c", "333", false)
	op(func() (uint64, error) { return e.Put([]byte("b"), []byte("2x")) }, "b", "2x", false)
	op(func() (uint64, error) { return e.Put([]byte("d"), []byte("4444")) }, "d", "4444", false)
	ids := sealedIDs(t, dir)
	if len(ids) < 3 {
		t.Fatalf("sealed=%v", ids)
	}
	before := dumpEngine(e)
	if !snapshotsEqual(model.snapshot(), before) {
		t.Fatal("model mismatch before merge")
	}
	// Merge first and third sealed segments, skipping the middle one.
	chosen := []int{ids[0], ids[2]}
	if _, err := e.Merge(chosen); err != nil {
		t.Fatal(err)
	}
	if !snapshotsEqual(model.snapshot(), dumpEngine(e)) {
		t.Fatal("state changed by merge")
	}
	e.Close()
	e2, err := Open(dir, Config{MaxSegmentBytes: 30, WriteHints: true})
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close()
	if !snapshotsEqual(model.snapshot(), dumpEngine(e2)) {
		t.Fatal("state differs after reopen")
	}
}

// TestMergeErrors checks argument / not-found / active-not-mergeable.
func TestMergeErrors(t *testing.T) {
	dir := t.TempDir()
	e, err := Open(dir, Config{MaxSegmentBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	e.Put([]byte("a"), []byte("1"))
	e.Put([]byte("b"), []byte("123456789012345678901234567890"))
	if _, err := e.Merge(nil); err == nil {
		t.Fatal("nil merge accepted")
	} else if ke, _ := AsError(err); ke.Kind != KindInvalidArgument {
		t.Fatalf("kind=%v", ke.Kind)
	}
	if _, err := e.Merge([]int{99}); err == nil {
		t.Fatal("missing accepted")
	} else if ke, _ := AsError(err); ke.Kind != KindSegmentNotFound {
		t.Fatalf("kind=%v", ke.Kind)
	}
	active := e.activeSegment().id
	if _, err := e.Merge([]int{active}); err == nil {
		t.Fatal("active merge accepted")
	} else if ke, _ := AsError(err); ke.Kind != KindActiveSegmentNotMergeable {
		t.Fatalf("kind=%v", ke.Kind)
	}
	// Duplicate id.
	ids := sealedIDs(t, dir)
	if len(ids) != 0 {
		if _, err := e.Merge([]int{ids[0], ids[0]}); err == nil {
			t.Fatal("dup accepted")
		}
	}
}

// TestMergeOldValueNoResurrect specifically verifies that merging an old
// value segment while keeping the tombstone segment out preserves deletion.
func TestMergeOldValueNoResurrect(t *testing.T) {
	dir := t.TempDir()
	e, err := Open(dir, Config{MaxSegmentBytes: 30})
	if err != nil {
		t.Fatal(err)
	}
	e.Put([]byte("k"), []byte("old"))
	if err := e.forceSeal(); err != nil {
		t.Fatal(err)
	}
	e.Delete([]byte("k"))
	if err := e.forceSeal(); err != nil {
		t.Fatal(err)
	}
	ids := sealedIDs(t, dir)
	if len(ids) != 2 {
		t.Fatalf("ids=%v", ids)
	}
	// Merge ONLY the old-value segment 1: it cannot resurrect k because the
	// tombstone in segment 2 survives with a larger seq.
	if _, err := e.Merge([]int{ids[0]}); err != nil {
		t.Fatal(err)
	}
	expectGet(t, e, "k", modelDeleted, "")
	e.Close()
	e2, _ := Open(dir, Config{MaxSegmentBytes: 1 << 20})
	defer e2.Close()
	expectGet(t, e2, "k", modelDeleted, "")
}

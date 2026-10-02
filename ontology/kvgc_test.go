package ontology

import (
	"bytes"
	"errors"
	"sync"
	"testing"
)

func mustWrite(t *testing.T, db *KVGC, key string, typ RecordType, ts int64, value string) {
	t.Helper()
	if err := db.Write([]byte(key), typ, ts, []byte(value)); err != nil {
		t.Fatalf("Write(%s,%c,%d,%q): %v", key, typ, ts, value, err)
	}
}

func getResult(t *testing.T, db *KVGC, key string, ts int64) (string, bool) {
	t.Helper()
	value, ok, err := db.Get([]byte(key), ts)
	if err != nil {
		t.Fatalf("Get(%s,%d): %v", key, ts, err)
	}
	return string(value), ok
}

func TestSpecExample(t *testing.T) {
	db := NewKVGC(100)
	mustWrite(t, db, "k", TypePut, 5, "a")
	mustWrite(t, db, "k", TypeLock, 7, "")
	mustWrite(t, db, "k", TypePut, 9, "b")
	mustWrite(t, db, "k", TypeDelete, 12, "")
	mustWrite(t, db, "k", TypeRollback, 13, "")
	mustWrite(t, db, "k", TypePut, 15, "c")

	if value, ok := getResult(t, db, "k", 11); !ok || value != "b" {
		t.Fatalf("Get(k,11) = %q,%v", value, ok)
	}
	if value, ok := getResult(t, db, "k", 13); ok || value != "" {
		t.Fatalf("Get(k,13) = %q,%v, want absent", value, ok)
	}

	if err := db.SetSafePoint(10); err != nil {
		t.Fatal(err)
	}
	count, err := db.GCStep(10)
	if err != nil || count != 1 {
		t.Fatalf("GCStep = %d,%v", count, err)
	}
	if got := db.accessCounts["k"]; got != 3 {
		t.Fatalf("access count = %d, want 3", got)
	}
	assertRecordTypes(t, db, "k", 9, 12, 13, 15)

	if err := db.SetSafePoint(13); err != nil {
		t.Fatal(err)
	}
	count, err = db.GCStep(10)
	if err != nil || count != 1 {
		t.Fatalf("GCStep = %d,%v", count, err)
	}
	if got := db.accessCounts["k"]; got != 6 {
		t.Fatalf("access count = %d, want 6", got)
	}
	assertRecordTypes(t, db, "k", 15)

	if value, ok := getResult(t, db, "k", 14); ok || value != "" {
		t.Fatalf("Get(k,14) = %q,%v, want absent", value, ok)
	}
	if value, ok := getResult(t, db, "k", 15); !ok || value != "c" {
		t.Fatalf("Get(k,15) = %q,%v", value, ok)
	}
	if _, _, err := db.Get([]byte("k"), 12); !errors.Is(err, ErrExpired) {
		t.Fatalf("Get before safe point error = %v, want ErrExpired", err)
	}
}

func assertRecordTypes(t *testing.T, db *KVGC, key string, timestamps ...int64) {
	t.Helper()
	state := db.keys[key]
	if state == nil {
		if len(timestamps) != 0 {
			t.Fatalf("key %s disappeared, want %v", key, timestamps)
		}
		return
	}
	if len(state.records) != len(timestamps) {
		t.Fatalf("timestamps = %v, want %v", recordTimestamps(state.records), timestamps)
	}
	for index, ts := range timestamps {
		if state.records[index].ts != ts {
			t.Fatalf("timestamps = %v, want %v", recordTimestamps(state.records), timestamps)
		}
	}
}

func recordTimestamps(records []record) []int64 {
	result := make([]int64, len(records))
	for index, current := range records {
		result[index] = current.ts
	}
	return result
}

func TestGCCasesAndCursor(t *testing.T) {
	db := NewKVGC(100)
	mustWrite(t, db, "a", TypePut, 4, "keep")
	mustWrite(t, db, "a", TypeLock, 5, "")
	mustWrite(t, db, "a", TypeRollback, 6, "")
	mustWrite(t, db, "a", TypePut, 7, "future")
	mustWrite(t, db, "a", TypeLock, 8, "")
	mustWrite(t, db, "a", TypePut, 9, "later")
	mustWrite(t, db, "b", TypePut, 2, "old")
	mustWrite(t, db, "b", TypeLock, 3, "")
	mustWrite(t, db, "b", TypeRollback, 4, "")
	mustWrite(t, db, "b", TypeDelete, 5, "")
	mustWrite(t, db, "c", TypeLock, 3, "")
	mustWrite(t, db, "c", TypeRollback, 4, "")
	mustWrite(t, db, "c", TypePut, 8, "future")
	mustWrite(t, db, "d", TypePut, 1, "old")
	mustWrite(t, db, "d", TypeDelete, 2, "")

	if err := db.SetSafePoint(5); err != nil {
		t.Fatal(err)
	}
	count, err := db.GCStep(2)
	if err != nil || count != 2 {
		t.Fatalf("GCStep = %d,%v", count, err)
	}
	assertRecordTypes(t, db, "a", 4, 6, 7, 8, 9)
	assertRecordTypes(t, db, "b")
	if !db.hasCursor || !bytes.Equal(db.cursor, []byte("b")) {
		t.Fatalf("cursor = %q, want b", db.cursor)
	}

	mustWrite(t, db, "a0", TypePut, 6, "after cursor")
	mustWrite(t, db, "d", TypePut, 9, "new future")
	count, err = db.GCStep(10)
	if err != nil || count != 2 {
		t.Fatalf("GCStep = %d,%v, want c,d", count, err)
	}
	assertRecordTypes(t, db, "c", 8)
	assertRecordTypes(t, db, "d", 9)
	assertRecordTypes(t, db, "a0", 6)

	count, err = db.GCStep(10)
	if err != nil || count != 0 {
		t.Fatalf("GCStep end = %d,%v", count, err)
	}

	if err := db.SetSafePoint(5); err != nil {
		t.Fatal(err)
	}
	count, err = db.GCStep(10)
	if err != nil || count != 4 {
		t.Fatalf("new round GCStep = %d,%v", count, err)
	}
	assertRecordTypes(t, db, "a0", 6)
	assertRecordTypes(t, db, "c", 8)
	assertRecordTypes(t, db, "a", 4, 6, 7, 8, 9)
	if db.total != 8 {
		t.Fatalf("total = %d, want 8", db.total)
	}
}

func TestSafePointSnapshotsAndErrors(t *testing.T) {
	db := NewKVGC(2)
	snapshot, err := db.OpenSnapshot(10)
	if err != nil || snapshot != 1 {
		t.Fatalf("OpenSnapshot = %d,%v", snapshot, err)
	}
	if err := db.SetSafePoint(10); err != nil {
		t.Fatalf("safe point equal to snapshot: %v", err)
	}
	if err := db.SetSafePoint(11); !errors.Is(err, ErrSnapshotBlocked) {
		t.Fatalf("SetSafePoint(11) = %v, want blocked", err)
	}
	if err := db.CloseSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	if err := db.CloseSnapshot(snapshot); !errors.Is(err, ErrSnapshotNotFound) {
		t.Fatalf("double close = %v", err)
	}

	if err := db.Write(nil, TypePut, 12, nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty key = %v", err)
	}
	if err := db.Write([]byte("k"), TypeDelete, 12, []byte("x")); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("delete value = %v", err)
	}
	if err := db.Write([]byte("k"), TypePut, 10, nil); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired write = %v", err)
	}
	mustWrite(t, db, "k", TypePut, 12, "x")
	if err := db.Write([]byte("k"), TypeLock, 12, nil); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate = %v", err)
	}
	mustWrite(t, db, "q", TypePut, 13, "")
	if err := db.Write([]byte("z"), TypePut, 14, nil); !errors.Is(err, ErrFull) {
		t.Fatalf("full = %v", err)
	}
	if _, _, err := db.Get(nil, 11); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid get = %v", err)
	}
	if _, _, err := db.Get([]byte("k"), 9); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired get = %v", err)
	}
	if _, err := db.GCStep(0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid GCStep = %v", err)
	}
	if err := db.SetSafePoint(9); !errors.Is(err, ErrRolledBack) {
		t.Fatalf("rollback = %v", err)
	}
}

func TestExactSafePointPutAndInRoundNewKey(t *testing.T) {
	db := NewKVGC(100)
	mustWrite(t, db, "a", TypePut, 5, "at-safe-point")
	mustWrite(t, db, "b", TypePut, 5, "old")
	if err := db.SetSafePoint(5); err != nil {
		t.Fatal(err)
	}
	if count, err := db.GCStep(1); err != nil || count != 1 {
		t.Fatalf("GCStep = %d,%v", count, err)
	}
	assertRecordTypes(t, db, "a", 5)
	if value, ok := getResult(t, db, "a", 5); !ok || value != "at-safe-point" {
		t.Fatalf("Get(a,5) = %q,%v", value, ok)
	}

	mustWrite(t, db, "c", TypePut, 6, "new-after-cursor")
	if count, err := db.GCStep(2); err != nil || count != 2 {
		t.Fatalf("GCStep with new key = %d,%v", count, err)
	}
	assertRecordTypes(t, db, "c", 6)
	if count, err := db.GCStep(2); err != nil || count != 0 {
		t.Fatalf("round end = %d,%v", count, err)
	}
}

func TestConcurrentWritesAndReads(t *testing.T) {
	db := NewKVGC(10000)
	var wait sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			for index := 0; index < 100; index++ {
				key := []byte{byte(worker), byte(index)}
				if err := db.Write(key, TypePut, int64(worker*100+index+1), key); err != nil {
					t.Errorf("Write: %v", err)
					return
				}
				if _, _, err := db.Get(key, int64(worker*100+index+1)); err != nil {
					t.Errorf("Get: %v", err)
					return
				}
			}
		}(worker)
	}
	wait.Wait()
	if db.total != 1600 {
		t.Fatalf("total = %d, want 1600", db.total)
	}
}

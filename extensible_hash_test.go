package ontology

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func lowBitHash(values map[string]uint64) HashFunc {
	return func(key string) uint64 {
		return values[key]
	}
}

func logOperation(t *testing.T, name string, snapshot Snapshot, err error) {
	t.Helper()
	t.Logf("input=%s output_err=%v global_depth=%d directory=%v buckets=%v counters=%+v",
		name, err, snapshot.GlobalDepth, snapshot.Directory, snapshot.Buckets, snapshot.Counters)
}

func assertInvariant(t *testing.T, snapshot Snapshot, capacity int) {
	t.Helper()

	expectedEntries := 1 << snapshot.GlobalDepth
	if len(snapshot.Directory) != expectedEntries {
		t.Fatalf("directory size mismatch: got %d, want %d", len(snapshot.Directory), expectedEntries)
	}

	byID := make(map[int]BucketSnapshot, len(snapshot.Buckets))
	for _, current := range snapshot.Buckets {
		byID[current.ID] = current
	}

	references := make(map[int]int)
	for _, bucketID := range snapshot.Directory {
		current, ok := byID[bucketID]
		if !ok {
			t.Fatalf("directory references missing bucket %d", bucketID)
		}
		if len(current.Keys) > capacity {
			t.Fatalf("bucket %d over capacity: %d > %d", bucketID, len(current.Keys), capacity)
		}
		references[bucketID]++
	}

	for id, count := range references {
		current := byID[id]
		want := 1 << (snapshot.GlobalDepth - current.LocalDepth)
		if count != want {
			t.Fatalf("bucket %d reference count mismatch: got %d, want %d", id, count, want)
		}
	}

	for _, current := range snapshot.Buckets {
		if current.LocalDepth > snapshot.GlobalDepth {
			t.Fatalf("bucket %d local depth %d exceeds global depth %d", current.ID, current.LocalDepth, snapshot.GlobalDepth)
		}
		if references[current.ID] == 0 {
			t.Fatalf("unreferenced bucket %d", current.ID)
		}
	}
}

func TestNewSnapshotInvariant(t *testing.T) {
	index, err := New(Config{Capacity: 2, MaxBuckets: 4, HashBits: 4})
	if err != nil {
		t.Fatal(err)
	}

	before := index.Snapshot()
	assertInvariant(t, before, 2)
	want := Snapshot{
		GlobalDepth: 0,
		Directory:   []int{1},
		Buckets: []BucketSnapshot{{
			ID:         1,
			LocalDepth: 0,
			Keys:       []string{},
		}},
	}
	if !reflect.DeepEqual(before, want) {
		t.Fatalf("initial snapshot mismatch:\ngot  %#v\nwant %#v", before, want)
	}
}

func TestSplitOverflowRollbackCascadeMergeAndShrink(t *testing.T) {
	hashes := map[string]uint64{
		"k1":       0,
		"k2":       4,
		"k3":       12,
		"k4":       8,
		"overflow": 16,
	}
	index, err := New(Config{
		Hash:       lowBitHash(hashes),
		Capacity:   1,
		MaxBuckets: 8,
		HashBits:   4,
	})
	if err != nil {
		t.Fatal(err)
	}

	naive := make(map[string]bool)
	insertKey := func(key string) error {
		err := index.Insert(key)
		if !naive[key] && err == nil {
			naive[key] = true
		}
		snapshot := index.Snapshot()
		assertInvariant(t, snapshot, 1)
		logOperation(t, "insert "+key, snapshot, err)
		return err
	}
	deleteKey := func(key string) error {
		err := index.Delete(key)
		if naive[key] && err == nil {
			delete(naive, key)
		}
		snapshot := index.Snapshot()
		assertInvariant(t, snapshot, 1)
		logOperation(t, "delete "+key, snapshot, err)
		return err
	}

	for _, key := range []string{"k1", "k2", "k3", "k4"} {
		if err := insertKey(key); err != nil {
			t.Fatalf("insert %s: %v", key, err)
		}
	}

	snapshot := index.Snapshot()
	if snapshot.Counters.Doubles != 4 || snapshot.Counters.Splits != 5 {
		t.Fatalf("growth counters mismatch: got doubles=%d splits=%d, want 4 and 5",
			snapshot.Counters.Doubles, snapshot.Counters.Splits)
	}

	beforeOverflow := index.Snapshot()
	err = insertKey("overflow")
	afterOverflow := index.Snapshot()
	logOperation(t, "insert overflow", afterOverflow, err)
	if !errors.Is(err, ErrBucketOverflow) {
		t.Fatalf("overflow error mismatch: got %v, want %v", err, ErrBucketOverflow)
	}
	if !reflect.DeepEqual(afterOverflow, beforeOverflow) {
		t.Fatalf("overflow changed state:\nbefore %#v\nafter  %#v", beforeOverflow, afterOverflow)
	}

	if err := insertKey("k1"); !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("duplicate error mismatch: got %v, want %v", err, ErrDuplicateKey)
	}
	if err := deleteKey("missing"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("missing delete error mismatch: got %v, want %v", err, ErrKeyNotFound)
	}
	if err := index.Lookup("missing"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("missing lookup error mismatch: got %v, want %v", err, ErrKeyNotFound)
	}

	for _, key := range []string{"k3", "k4", "k2", "k1"} {
		if err := deleteKey(key); err != nil {
			t.Fatalf("delete %s: %v", key, err)
		}
	}

	final := index.Snapshot()
	wantFinal := Snapshot{
		GlobalDepth: 0,
		Directory:   []int{1},
		Buckets: []BucketSnapshot{{
			ID:         1,
			LocalDepth: 0,
			Keys:       []string{},
		}},
		Counters: Counters{
			Splits:  5,
			Merges:  5,
			Doubles: 4,
			Halves:  4,
		},
	}
	if !reflect.DeepEqual(final, wantFinal) {
		t.Fatalf("final snapshot mismatch:\ngot  %#v\nwant %#v", final, wantFinal)
	}
	if len(naive) != 0 {
		t.Fatalf("naive map mismatch: %v", naive)
	}
}

func TestBucketLimitRejectsAtomically(t *testing.T) {
	index, err := New(Config{
		Hash: func(key string) uint64 {
			if key == "a" {
				return 0
			}
			return 1
		},
		Capacity:   1,
		MaxBuckets: 1,
		HashBits:   4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := index.Insert("a"); err != nil {
		t.Fatal(err)
	}

	before := index.Snapshot()
	err = index.Insert("b")
	after := index.Snapshot()
	logOperation(t, "insert b at bucket limit", after, err)
	if !errors.Is(err, ErrTooManyBuckets) {
		t.Fatalf("bucket limit error mismatch: got %v, want %v", err, ErrTooManyBuckets)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("rejected bucket-limit insert changed state:\nbefore %#v\nafter  %#v", before, after)
	}
}

func TestDeterministicReplay(t *testing.T) {
	type operation struct {
		kind string
		key  string
	}

	operations := []operation{
		{"insert", "a"}, {"insert", "b"}, {"insert", "c"},
		{"insert", "d"}, {"insert", "a"}, {"delete", "c"},
		{"delete", "missing"}, {"delete", "d"}, {"insert", "c"},
	}
	hashes := map[string]uint64{"a": 0, "b": 4, "c": 12, "d": 8}

	replay := func() []Snapshot {
		index, err := New(Config{
			Hash:       lowBitHash(hashes),
			Capacity:   2,
			MaxBuckets: 8,
			HashBits:   4,
		})
		if err != nil {
			t.Fatal(err)
		}

		snapshots := make([]Snapshot, 0, len(operations))
		for _, current := range operations {
			var err error
			switch current.kind {
			case "insert":
				err = index.Insert(current.key)
			case "delete":
				err = index.Delete(current.key)
			}
			snapshot := index.Snapshot()
			assertInvariant(t, snapshot, 2)
			logOperation(t, current.kind+" "+current.key, snapshot, err)
			snapshots = append(snapshots, snapshot)
		}
		return snapshots
	}

	first := replay()
	second := replay()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("replay snapshots differ:\nfirst  %#v\nsecond %#v", first, second)
	}
}

func TestConcurrentOperations(t *testing.T) {
	index, err := New(Config{Capacity: 8, MaxBuckets: 256, HashBits: 12})
	if err != nil {
		t.Fatal(err)
	}

	var workers sync.WaitGroup
	start := make(chan struct{})
	for worker := 0; worker < 8; worker++ {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			<-start
			for item := 0; item < 40; item++ {
				key := fmt.Sprintf("core-%d-%d", worker, item)
				if err := index.Insert(key); err != nil {
					t.Errorf("insert %s: %v", key, err)
					return
				}
				snapshot := index.Snapshot()
				assertInvariant(t, snapshot, 8)
			}
		}(worker)
	}
	close(start)
	workers.Wait()

	for worker := 0; worker < 4; worker++ {
		key := fmt.Sprintf("ephemeral-%d", worker)
		if err := index.Insert(key); err != nil {
			t.Fatal(err)
		}
	}

	start = make(chan struct{})
	for worker := 0; worker < 8; worker++ {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			<-start
			for item := 0; item < 40; item++ {
				key := fmt.Sprintf("core-%d-%d", worker, item)
				if err := index.Lookup(key); err != nil {
					t.Errorf("lookup %s: %v", key, err)
					return
				}
				snapshot := index.Snapshot()
				assertInvariant(t, snapshot, 8)
			}
		}(worker)
	}
	for worker := 0; worker < 4; worker++ {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			<-start
			key := fmt.Sprintf("ephemeral-%d", worker)
			if err := index.Delete(key); err != nil {
				t.Errorf("delete %s: %v", key, err)
			}
			snapshot := index.Snapshot()
			assertInvariant(t, snapshot, 8)
			logOperation(t, "delete "+key, snapshot, err)
		}(worker)
	}
	close(start)
	workers.Wait()

	for worker := 0; worker < 8; worker++ {
		for item := 0; item < 40; item++ {
			key := fmt.Sprintf("core-%d-%d", worker, item)
			if err := index.Lookup(key); err != nil {
				t.Fatalf("final lookup %s: %v", key, err)
			}
		}
	}
	for worker := 0; worker < 4; worker++ {
		key := fmt.Sprintf("ephemeral-%d", worker)
		if err := index.Lookup(key); !errors.Is(err, ErrKeyNotFound) {
			t.Fatalf("deleted key lookup mismatch: got %v, want %v", err, ErrKeyNotFound)
		}
	}
	assertInvariant(t, index.Snapshot(), 8)
}

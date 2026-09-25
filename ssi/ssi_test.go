package ssi

import (
	"errors"
	"sync"
	"testing"

	"ontology/kv"
)

// TestComplexityExaminedCount: with m committed readers of one hot key,
// committing a writer of that key must examine a number of committed
// transactions bounded by a small constant independent of m (index probes
// visit no records at all).
func TestComplexityExaminedCount(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		mgr := NewManager(kv.New(map[string]string{"hot": "0"}))
		for i := 0; i < m; i++ {
			r := mgr.Begin()
			if _, err := r.Read("hot"); err != nil {
				t.Fatalf("m=%d read: %v", m, err)
			}
			if err := r.Commit(); err != nil {
				t.Fatalf("m=%d reader commit: %v", m, err)
			}
		}
		w := mgr.Begin()
		if err := w.Write("hot", "1"); err != nil {
			t.Fatalf("m=%d write: %v", m, err)
		}
		if err := w.Commit(); err != nil { // in-only conflict: must commit
			t.Fatalf("m=%d writer commit: %v", m, err)
		}
		if mgr.examined > 2 {
			t.Fatalf("m=%d examined=%d, want <= 2 (independent of m)", m, mgr.examined)
		}
		if mgr.store.Committed()["hot"] != "1" {
			t.Fatalf("m=%d writer not applied", m)
		}
	}
}

// TestConflictFlags: the later committer of a write-skew pair rolls back
// with both rw flags set; uninvolved txns keep both flags clear.
func TestConflictFlags(t *testing.T) {
	mgr := NewManager(kv.New(map[string]string{"x": "1", "y": "1"}))
	t1, t2 := mgr.Begin(), mgr.Begin()
	for _, op := range []func() error{
		func() error { _, e := t1.Read("x"); return e },
		func() error { return t1.Write("y", "2") },
		func() error { _, e := t2.Read("y"); return e },
		func() error { return t2.Write("x", "2") },
		func() error { return t1.Commit() },
	} {
		if err := op(); err != nil {
			t.Fatal(err)
		}
	}
	if err := t2.Commit(); !errors.Is(err, ErrConflict) {
		t.Fatalf("t2 commit = %v, want ErrConflict", err)
	}
	if in, out := t2.Flags(); !in || !out {
		t.Fatalf("t2 flags = %v,%v, want true,true", in, out)
	}
	if in, out := t1.Flags(); in || out {
		t.Fatalf("t1 flags = %v,%v, want false,false", in, out)
	}
}

// TestReadYourWrites: own writes are visible to reads and are NOT recorded
// in the retained read set.
func TestReadYourWrites(t *testing.T) {
	mgr := NewManager(kv.New(map[string]string{"z": "5"}))
	tx := mgr.Begin()
	if err := tx.Write("z", "99"); err != nil {
		t.Fatal(err)
	}
	if v, err := tx.Read("z"); err != nil || v != "99" {
		t.Fatalf("read-own-write = %q, %v; want 99", v, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	recs := mgr.store.Records()
	if len(recs) != 1 || len(recs[0].ReadSet) != 0 {
		t.Fatalf("read set retained = %v, want empty", recs[0].ReadSet)
	}
	if v := mgr.Committed()["z"]; v != "99" {
		t.Fatalf("committed z = %q, want 99", v)
	}
}

// TestConcurrentSnapshots: readers that began before a concurrent commit
// all see the identical pre-commit snapshot (per key), never torn values;
// after the commit, new state is visible. Run with -race.
func TestConcurrentSnapshots(t *testing.T) {
	keys := []string{"a", "b", "c"}
	mgr := NewManager(kv.New(map[string]string{"a": "old", "b": "old", "c": "old"}))
	const n = 8
	began := make(chan struct{}, n)
	release := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tx := mgr.Begin()
			began <- struct{}{}
			<-release // commit lands between Begin and Read
			for _, k := range keys {
				if v, err := tx.Read(k); err != nil || v != "old" {
					t.Errorf("reader saw %q,%v; want identical old snapshot", v, err)
				}
			}
		}()
	}
	for i := 0; i < n; i++ {
		<-began
	}
	w := mgr.Begin()
	for _, k := range keys {
		_ = w.Write(k, "new")
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}
	close(release)
	wg.Wait()
	if got := mgr.Committed()["a"]; got != "new" {
		t.Fatalf("final = %q, want new", got)
	}
}

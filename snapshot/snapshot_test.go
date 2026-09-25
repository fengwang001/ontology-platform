package snapshot_test

import (
	"fmt"
	"sync"
	"testing"

	"ontology/snapshot"
	"ontology/store"
)

func TestSnapshotSemantics(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"empty store", func(t *testing.T) {
			snap := snapshot.Take(store.New())
			defer snap.Close()
			if len(snap.Keys()) != 0 {
				t.Fatalf("empty store should have no keys")
			}
			if _, _, ok := snap.Get("missing"); ok {
				t.Fatalf("missing key must be distinguishable")
			}
		}},
		{"single key", func(t *testing.T) {
			st := store.New()
			st.Put("a", []byte("1"))
			snap := snapshot.Take(st)
			defer snap.Close()
			if v, p, ok := snap.Get("a"); !ok || !p || string(v) != "1" {
				t.Fatalf("single key read wrong: %q %v %v", v, p, ok)
			}
		}},
		{"empty key and empty value", func(t *testing.T) {
			st := store.New()
			st.Put("", []byte{})
			snap := snapshot.Take(st)
			defer snap.Close()
			if v, p, ok := snap.Get(""); !ok || !p || len(v) != 0 {
				t.Fatalf("empty key/value must be present")
			}
		}},
		{"old value preserved during overwrite", func(t *testing.T) {
			st := store.New()
			st.Put("k", []byte("old"))
			snap := snapshot.Take(st)
			defer snap.Close()
			st.Put("k", []byte("new"))
			if v, _, _ := snap.Get("k"); string(v) != "old" {
				t.Fatalf("snapshot must see old value, got %q", v)
			}
		}},
		{"all keys overwritten", func(t *testing.T) {
			st := store.New()
			for i := 0; i < 10; i++ {
				st.Put(fmt.Sprintf("k%d", i), []byte("v"))
			}
			snap := snapshot.Take(st)
			for i := 0; i < 10; i++ {
				st.Put(fmt.Sprintf("k%d", i), []byte("w"))
			}
			if got := snap.Retained(); got != 10 {
				t.Fatalf("retained = %d, want 10", got)
			}
			snap.Close()
		}},
	}
	for _, c := range cases {
		t.Run(c.name, c.run)
	}
}

func TestCOWDuringExport(t *testing.T) {
	st := store.New()
	for i := 0; i < 100; i++ {
		st.Put(fmt.Sprintf("k%03d", i), []byte(fmt.Sprintf("v%d", i)))
	}
	snap := snapshot.Take(st)
	defer snap.Close()
	keys := snap.OrderedKeys(true)
	for i := 0; i < 50; i++ {
		snap.Get(keys[i])
	}
	st.Put("k079", []byte("LATEST"))
	for i := 50; i < len(keys); i++ {
		snap.Get(keys[i])
	}
	if v, _, _ := snap.Get("k079"); string(v) != "v79" {
		t.Fatalf("key 80 must be snapshot-old value, got %q", v)
	}
	if snap.Reads() != int64(len(keys))+1 {
		t.Fatalf("reads = %d, want %d", snap.Reads(), len(keys)+1)
	}
}

func TestRetainedLifecycle(t *testing.T) {
	t.Run("close releases all", func(t *testing.T) {
		st := store.New()
		for i := 0; i < 10; i++ {
			st.Put(fmt.Sprintf("k%d", i), []byte("v"))
		}
		snap := snapshot.Take(st)
		for i := 0; i < 10; i++ {
			st.Put(fmt.Sprintf("k%d", i), []byte("w"))
		}
		snap.Close()
		if got := st.Retained(); got != 0 {
			t.Fatalf("retained after close = %d, want 0", got)
		}
	})
	t.Run("overlapping snapshots", func(t *testing.T) {
		st := store.New()
		st.Put("a", []byte("0"))
		old := snapshot.Take(st)
		st.Put("a", []byte("1"))
		newSnap := snapshot.Take(st)
		st.Put("a", []byte("2"))
		newSnap.Close()
		if got := st.Retained(); got == 0 {
			t.Fatalf("old snapshot still active, retained must be > 0")
		}
		old.Close()
		if got := st.Retained(); got != 0 {
			t.Fatalf("retained after both close = %d, want 0", got)
		}
	})
	t.Run("bounded by overwritten keys", func(t *testing.T) {
		st := store.New()
		for i := 0; i < 100000; i++ {
			st.Put(fmt.Sprintf("k%06d", i), []byte("v"))
		}
		snap := snapshot.Take(st)
		defer snap.Close()
		for i := 0; i < 100; i++ {
			st.Put(fmt.Sprintf("k%06d", i), []byte("w"))
		}
		if got := snap.Retained(); got > 100 {
			t.Fatalf("retained = %d, must be <= 100", got)
		}
	})
}

func TestConcurrentAccess(t *testing.T) {
	st := store.New()
	for i := 0; i < 200; i++ {
		st.Put(fmt.Sprintf("k%d", i), []byte("v"))
	}
	snaps := make([]*snapshot.Snapshot, 4)
	for i := range snaps {
		snaps[i] = snapshot.Take(st)
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				st.Put(fmt.Sprintf("k%d", (i+g*7)%200), []byte("x"))
			}
		}(g)
	}
	for _, snap := range snaps {
		wg.Add(1)
		go func(snap *snapshot.Snapshot) {
			defer wg.Done()
			for _, k := range snap.Keys() {
				snap.Get(k)
			}
		}(snap)
	}
	wg.Wait()
	for _, snap := range snaps {
		snap.Close()
	}
}

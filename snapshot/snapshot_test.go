package snapshot_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/snapshot"
	"ontology/store"
)

func TestBoundary(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"empty key is legal", func(t *testing.T) {
			st := store.New()
			st.Put("", []byte("v"))
			if v, ok := st.Get(""); !ok || string(v) != "v" {
				t.Fatalf("empty key: got %q,%v", v, ok)
			}
		}},
		{"empty value differs from absent", func(t *testing.T) {
			st := store.New()
			st.Put("k", []byte{})
			if v, ok := st.Get("k"); !ok || len(v) != 0 {
				t.Fatalf("empty value: got %q,%v", v, ok)
			}
			if _, ok := st.Get("missing"); ok {
				t.Fatal("absent key reported present")
			}
		}},
		{"snapshot keeps overwritten old value", func(t *testing.T) {
			st := store.New()
			st.Put("k", []byte("old"))
			snap := snapshot.Open(st)
			defer snap.Close()
			st.Put("k", []byte("new"))
			if v, ok, _ := snap.Get("k"); !ok || string(v) != "old" {
				t.Fatalf("snapshot read: got %q,%v", v, ok)
			}
		}},
		{"key created after snapshot invisible", func(t *testing.T) {
			st := store.New()
			snap := snapshot.Open(st)
			defer snap.Close()
			st.Put("late", []byte("v"))
			if _, ok, _ := snap.Get("late"); ok {
				t.Fatal("late key visible in snapshot")
			}
			keys, _ := snap.Keys()
			if len(keys) != 0 {
				t.Fatalf("snapshot keys: %v", keys)
			}
		}},
		{"closed snapshot errors", func(t *testing.T) {
			st := store.New()
			snap := snapshot.Open(st)
			snap.Close()
			if _, _, err := snap.Get("k"); !errors.Is(err, snapshot.ErrClosed) {
				t.Fatalf("Get after close: %v", err)
			}
			if _, err := snap.Keys(); !errors.Is(err, snapshot.ErrClosed) {
				t.Fatalf("Keys after close: %v", err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, tc.run)
	}
}

func TestRetainedLifecycle(t *testing.T) {
	cases := []struct {
		name    string
		total   int
		modify  int
		wantMax int
	}{
		{"no writes during export", 100000, 0, 0},
		{"few writes during export", 100000, 100, 100},
		{"all keys rewritten", 100000, 100000, 100000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := store.New()
			for i := 0; i < tc.total; i++ {
				st.Put(fmt.Sprintf("k%06d", i), []byte("v"))
			}
			snap := snapshot.Open(st)
			for i := 0; i < tc.modify; i++ {
				st.Put(fmt.Sprintf("k%06d", i), []byte("new"))
			}
			if n := st.RetainedCount(); n > tc.wantMax {
				t.Fatalf("retained %d, want <= %d", n, tc.wantMax)
			}
			snap.Close()
			if n := st.RetainedCount(); n != 0 {
				t.Fatalf("retained %d after close", n)
			}
		})
	}
}

func TestOverlappingSnapshots(t *testing.T) {
	st := store.New()
	st.Put("a", []byte("v0"))
	s1 := snapshot.Open(st)
	st.Put("a", []byte("v1"))
	s2 := snapshot.Open(st)
	st.Put("a", []byte("v2"))
	s1.Close()
	if st.RetainedCount() == 0 {
		t.Fatal("released while s2 still open")
	}
	if v, ok, _ := s2.Get("a"); !ok || string(v) != "v1" {
		t.Fatalf("s2 read: got %q,%v", v, ok)
	}
	s2.Close()
	if n := st.RetainedCount(); n != 0 {
		t.Fatalf("retained %d after both closed", n)
	}
}

func TestConcurrentReadWrite(t *testing.T) {
	st := store.New()
	for i := 0; i < 100; i++ {
		st.Put(fmt.Sprintf("k%03d", i), []byte("v0"))
	}
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				st.Put(fmt.Sprintf("k%03d", (id+i)%100), []byte("vx"))
			}
		}(w)
	}
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				snap := snapshot.Open(st)
				keys, err := snap.Keys()
				if err != nil {
					t.Error(err)
					return
				}
				for _, k := range keys {
					if _, _, err := snap.Get(k); err != nil {
						t.Error(err)
					}
				}
				snap.Close()
			}
		}()
	}
	wg.Wait()
	if n := st.RetainedCount(); n != 0 {
		t.Fatalf("retained %d after all snapshots closed", n)
	}
}

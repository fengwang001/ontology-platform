package store_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"ontology/store"
)

func TestIncrementalReclaim(t *testing.T) {
	e100 := buildHot(t, 100).Reclaim()
	e10000 := buildHot(t, 10000).Reclaim()
	t.Logf("examined: N=100 -> %d, N=10000 -> %d", e100, e10000)
	if e100 != e10000 {
		t.Fatalf("examined count grows with N: %d vs %d", e100, e10000)
	}
	if e100 > 50 { // 45 shadowed candidates, independent of N
		t.Fatalf("examined %d candidates, want only the 45 shadowed ones", e100)
	}
}

func TestWatermarkMonotonicAndFreshSnapshot(t *testing.T) {
	s := store.New(store.Options{})
	var last store.Stats
	for round := 0; round < 5; round++ {
		snap, _ := s.OpenSnapshot()
		put(t, s, "k", fmt.Sprint(round))
		s.Reclaim()
		cur := s.Stats()
		if cur.Watermark < last.Watermark {
			t.Fatalf("watermark decreased: %v -> %v", last.Watermark, cur.Watermark)
		}
		last = cur
		snap.Close()
	}
	s.Reclaim()
	if st, val := read(t, s, "k"); st != store.Present || val != "4" {
		t.Fatalf("fresh snapshot after reclaim: got %v %q, want 4", st, val)
	}
}

func TestCrashPoints(t *testing.T) {
	for point := 0; point <= 3; point++ {
		t.Run(fmt.Sprintf("point-%d", point), func(t *testing.T) {
			armed := false
			s := store.New(store.Options{CrashHook: func(stage int) {
				if armed && stage == point {
					panic("crash")
				}
			}})
			put(t, s, "k", "base")
			armed = true
			tx := s.Begin()
			if err := tx.Write("k", []byte("v2")); err != nil {
				t.Fatal(err)
			}
			func() { defer func() { _ = recover() }(); _ = tx.Commit() }()
			armed = false
			s.Recover()
			st, val := read(t, s, "k")
			want, wantTotal := "v2", 2 // point 3: fully visible
			if point < 3 {             // fully invisible, no residue
				want, wantTotal = "base", 1
			}
			if st != store.Present || val != want {
				t.Fatalf("got %v %q, want %q", st, val, want)
			}
			if got := s.Stats().TotalVersions; got != wantTotal {
				t.Fatalf("residue: total = %d, want %d", s.Stats().TotalVersions, wantTotal)
			}
			put(t, s, "k", "v3") // index must be consistent after recovery
			s.Reclaim()
			if st, val := read(t, s, "k"); st != store.Present || val != "v3" {
				t.Fatalf("post-recovery: got %v %q, want v3", st, val)
			}
		})
	}
}

func TestLimits(t *testing.T) {
	cases := []struct {
		name  string
		opts  store.Options
		setup func(t *testing.T, s *store.Store)
		op    func(s *store.Store) error
		want  error
	}{
		{"chain", store.Options{MaxChainLen: 1},
			func(t *testing.T, s *store.Store) { put(t, s, "k", "v") },
			func(s *store.Store) error { return s.Begin().Write("k", []byte("x")) },
			store.ErrChainTooLong},
		{"snapshots", store.Options{MaxSnapshots: 1},
			func(t *testing.T, s *store.Store) { _, _ = s.OpenSnapshot() },
			func(s *store.Store) error { _, err := s.OpenSnapshot(); return err },
			store.ErrTooManySnapshots},
		{"versions", store.Options{MaxVersions: 1},
			func(t *testing.T, s *store.Store) { put(t, s, "k", "v") },
			func(s *store.Store) error { return s.Begin().Write("other", []byte("x")) },
			store.ErrTooManyVersions},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := store.New(c.opts)
			c.setup(t, s)
			before := s.Stats()
			if err := c.op(s); !errors.Is(err, c.want) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
			after := s.Stats()
			if after.TotalVersions != before.TotalVersions || after.Watermark != before.Watermark {
				t.Fatal("rejected op changed versions or watermark")
			}
			if c.name == "snapshots" && after.ActiveSnapshots != before.ActiveSnapshots {
				t.Fatal("rejected op changed snapshot count")
			}
		})
	}
}

func TestQueriesStable(t *testing.T) {
	s := buildHot(t, 3)
	q1 := s.Stats()
	if q2 := s.Stats(); q1 != q2 {
		t.Fatalf("consecutive stats differ: %+v vs %+v", q1, q2)
	}
	if s.KeyStats("missing") != 0 {
		t.Fatal("unknown key stats nonzero")
	}
	if s.Stats() != q1 {
		t.Fatal("queries advanced state")
	}
}

func TestConcurrentAccess(t *testing.T) {
	s := store.New(store.Options{})
	put(t, s, "hot", "init")
	var wg sync.WaitGroup
	stop := make(chan struct{})
	stopped := func() bool {
		select {
		case <-stop:
			return true
		default:
			return false
		}
	}
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; !stopped(); i++ {
				if tx := s.Begin(); tx.Write("hot", []byte(fmt.Sprint(g, i))) == nil {
					_ = tx.Commit()
				}
			}
		}(g)
	}
	wg.Add(1)
	go func() { // snapshot churn + reclaim + reads
		defer wg.Done()
		for !stopped() {
			snap, _ := s.OpenSnapshot()
			s.Reclaim()
			if st, _ := s.Get(snap, "hot"); st != store.Present {
				panic("hot key vanished under snapshot")
			}
			snap.Close()
		}
	}()
	time.Sleep(100 * time.Millisecond)
	close(stop)
	wg.Wait()
}

func TestLongTransactionDoesNotBlockOthers(t *testing.T) {
	s := store.New(store.Options{})
	long := s.Begin()
	if err := long.Write("a", []byte("held")); err != nil {
		t.Fatal(err)
	}
	done := make(chan string, 1)
	go func() {
		put(t, s, "b", "free")
		_, val := read(t, s, "b")
		done <- val
	}()
	select {
	case val := <-done:
		if val != "free" {
			t.Fatalf("got %q, want free", val)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("long transaction blocked another key")
	}
	_ = long.Rollback()
}

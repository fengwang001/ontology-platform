package ontology

import (
	"fmt"
	"sync"
	"testing"
)

func TestConcurrentPushNoLossOrDuplicate(t *testing.T) {
	const (
		goroutines = 16
		perG       = 500
		space      = goroutines * perG
		k          = 128
	)
	s, _ := New(k, Desc)

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				id := fmt.Sprintf("g%02d-i%03d", g, i)
				s.Push(id, float64(g*perG+i))
			}
		}(g)
	}

	// Concurrent snapshots during the push phase.
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				snap := s.Snapshot()
				if len(snap) > k {
					t.Errorf("snapshot too large: %d", len(snap))
					return
				}
				seen := map[string]bool{}
				for _, e := range snap {
					if seen[e.ID] {
						t.Errorf("duplicate ID in snapshot: %s", e.ID)
						return
					}
					seen[e.ID] = true
				}
			}
		}
	}()

	wg.Wait()
	close(stop)

	if s.Len() != k {
		t.Fatalf("len = %d, want %d", s.Len(), k)
	}
	snap := s.Snapshot()
	seen := map[string]bool{}
	for _, e := range snap {
		if seen[e.ID] {
			t.Fatalf("duplicate ID: %s", e.ID)
		}
		seen[e.ID] = true
	}
	// All scores in Desc top K must be the largest k of the stream.
	minScore := snap[len(snap)-1].Score
	if minScore != float64(space-k) {
		t.Fatalf("worst held score = %v, want %d", minScore, space-k)
	}
}

func TestSnapshotIsolation(t *testing.T) {
	s, _ := New(3, Desc)
	s.Push("a", 1)
	s.Push("b", 2)
	snap := s.Snapshot()
	snap[0].ID = "tampered"
	snap[0].Score = 999

	again := s.Snapshot()
	for _, e := range again {
		if e.ID == "tampered" {
			t.Fatal("snapshot shares internal storage")
		}
	}
}

func TestConcurrentOverwriteSameID(t *testing.T) {
	s, _ := New(64, Desc)
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				s.Push("shared", float64(g*100+i))
			}
		}(g)
	}
	wg.Wait()

	snap := s.Snapshot()
	count := 0
	for _, e := range snap {
		if e.ID == "shared" {
			count++
		}
	}
	if count > 1 {
		t.Fatalf("shared ID appears %d times", count)
	}
}

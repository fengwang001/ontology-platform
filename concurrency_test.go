package ontology

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentOneToOne: N goroutines race to link the same ONE_TO_ONE
// source; exactly one must succeed.
func TestConcurrentOneToOne(t *testing.T) {
	s := newCardinalityStore(t)
	const n = 32
	var wg sync.WaitGroup
	successes := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			successes <- s.CreateLink("spouse", "p1", fmt.Sprintf("p%d", i+2))
		}(i)
	}
	wg.Wait()
	close(successes)
	ok := 0
	for err := range successes {
		if err == nil {
			ok++
		} else if !IsViolation(err, ViolationOneToOne) &&
			!IsViolation(err, ViolationEndpointNotFound) {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("exactly one create should succeed, got %d", ok)
	}
	if got := len(s.LinksFrom("spouse", "p1")); got != 1 {
		t.Fatalf("source has %d links, want 1", got)
	}
	if err := s.CheckInvariant(); err != nil {
		t.Fatalf("CheckInvariant: %v", err)
	}
}

// TestConcurrentMirrorInvariant: concurrent creates, deletes and cascade
// deletes must never expose a broken index mirror to a concurrent reader.
func TestConcurrentMirrorInvariant(t *testing.T) {
	s := NewStore()
	mustTypes(t, s, "N")
	mustLinkType(t, s, LinkType{Name: "e", Source: "N", Target: "N",
		Cardinality: ManyToMany, OnDelete: SetNull})
	const objs = 16
	for i := 0; i < objs; i++ {
		mustObjects(t, s, "N", fmt.Sprintf("n%d", i))
	}
	stop := make(chan struct{})
	var readers sync.WaitGroup
	for r := 0; r < 2; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if err := s.CheckInvariant(); err != nil {
					t.Errorf("invariant broken under concurrency: %v", err)
					return
				}
			}
		}()
	}
	var writers sync.WaitGroup
	for w := 0; w < 4; w++ {
		writers.Add(1)
		go func(w int) {
			defer writers.Done()
			for i := 0; i < 200; i++ {
				a := fmt.Sprintf("n%d", (w+i)%objs)
				b := fmt.Sprintf("n%d", (w+i+1)%objs)
				switch i % 3 {
				case 0:
					_ = s.CreateLink("e", a, b)
				case 1:
					_ = s.DeleteLink("e", a, b)
				case 2:
					id := fmt.Sprintf("tmp-%d-%d", w, i)
					if s.AddObject("N", id) == nil {
						_ = s.CreateLink("e", a, id)
						_ = s.DeleteObject(id)
					}
				}
			}
		}(w)
	}
	writers.Wait()
	close(stop)
	readers.Wait()
	if err := s.CheckInvariant(); err != nil {
		t.Fatalf("CheckInvariant: %v", err)
	}
}

// TestConcurrentBatchAndCreate: batches and single creates interleaved must
// keep the mirror intact.
func TestConcurrentBatchAndCreate(t *testing.T) {
	s := newGraphStore(t)
	mustObjects(t, s, "Person", "p1", "p2", "p3", "p4")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			b := &Batch{}
			if i%2 == 0 {
				b.CreateLink("knows", "p1", "p2").DeleteLink("knows", "p1", "p2")
			} else {
				b.CreateLink("knows", "p3", "p4").DeleteLink("knows", "p3", "p4")
			}
			for j := 0; j < 50; j++ {
				if err := s.ApplyBatch(b); err != nil {
					t.Errorf("ApplyBatch: %v", err)
					return
				}
			}
		}(i)
	}
	wg.Wait()
	if err := s.CheckInvariant(); err != nil {
		t.Fatalf("CheckInvariant: %v", err)
	}
}

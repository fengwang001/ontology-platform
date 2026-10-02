package changebuffer

import (
	"sync"
	"testing"
)

// TestConcurrentExercises runs all entry points in parallel. Under -race it
// detects data races; correctness is checked via invariants afterwards.
func TestConcurrentExercises(t *testing.T) {
	cb := mustNew(t, 1024, 8, 500)
	var wg sync.WaitGroup

	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for i := 0; i < 400; i++ {
				page := (seed*7 + i) % 12
				key := string(rune('a'+(i%6))) + "-" + string(rune('a'+seed%26))
				switch i % 9 {
				case 0:
					cb.Load(page)
				case 1:
					cb.Evict(page)
				case 2, 3:
					cb.Op(page, Insert, key, int64(1+(i%200)))
				case 4, 5, 6:
					cb.Op(page, DeleteMark, key, 0)
				default:
					cb.Op(page, Purge, key, 0)
				}
				cb.View(page % 12)
				cb.GlobalBufBytes()
				_, _ = cb.Bucket(page)
				if i%37 == 0 {
					if err := cb.CheckInvariants(); err != nil {
						t.Errorf("invariants: %v", err)
						return
					}
				}
			}
		}(w)
	}
	wg.Wait()

	if err := cb.CheckInvariants(); err != nil {
		t.Fatal(err)
	}

	// View must equal the post-Load result for every non-resident page.
	cb.mu.Lock()
	pages := make([]int, 0, len(cb.pages))
	for p := range cb.pages {
		pages = append(pages, p)
	}
	cb.mu.Unlock()
	for _, p := range pages {
		before, _ := cb.View(p)
		in, _ := cb.InPool(p)
		if !in {
			if err := cb.Load(p); err != nil {
				t.Fatalf("load %d: %v", p, err)
			}
			after, _ := cb.View(p)
			if !entriesEqual(before, after) {
				t.Fatalf("page %d: View != post-Load result: %v vs %v", p, before, after)
			}
		}
	}
}

// TestReplayDeterminism replays a fixed mixed sequence on two independent
// buffers and requires identical entries, queues and pool states.
func TestReplayDeterminism(t *testing.T) {
	play := func(cb *ChangeBuffer) {
		must := func(err error) {
			t.Helper()
			if err != nil {
				t.Fatal(err)
			}
		}
		_, err := cb.Op(0, Insert, "a", 100)
		must(err)
		_, err = cb.Op(1, Insert, "b", 50)
		must(err)
		_, err = cb.Op(0, DeleteMark, "a", 0)
		must(err)
		_, err = cb.Op(0, Purge, "a", 0)
		must(err)
		must(cb.Load(0))
		_, err = cb.Op(0, Insert, "c", 30)
		must(err)
		must(cb.Evict(0))
		_, err = cb.Op(0, Insert, "d", 40)
		must(err)
		cb.Op(0, Insert, "e", 1024) // likely rejected
		_, err = cb.Op(2, Purge, "z", 0)
		must(err)
		cb.Evict(5) // not-in-pool error
		_, err = cb.Op(1, Insert, "a", 60)
		must(err)
	}

	cb1 := mustNew(t, 1024, 4, 150)
	cb2 := mustNew(t, 1024, 4, 150)
	play(cb1)
	play(cb2)

	s1, s2 := snapshot(cb1), snapshot(cb2)
	if len(s1) != len(s2) {
		t.Fatalf("page sets differ: %d vs %d", len(s1), len(s2))
	}
	for p, a := range s1 {
		b, ok := s2[p]
		if !ok {
			t.Fatalf("page %d missing in replay", p)
		}
		if a.inPool != b.inPool || a.used != b.used || a.bufBytes != b.bufBytes {
			t.Fatalf("page %d state differs: %+v vs %+v", p, a, b)
		}
		if !queueEqual(a.queue, b.queue) {
			t.Fatalf("page %d queue differs: %v vs %v", p, a.queue, b.queue)
		}
		if len(a.entries) != len(b.entries) {
			t.Fatalf("page %d entry sets differ", p)
		}
		for _, ea := range a.entries {
			found := false
			for _, eb := range b.entries {
				if ea == eb {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("page %d entry %v missing in replay", p, ea)
			}
		}
	}
}

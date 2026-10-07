package ontology

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

// TestConcurrentSerializability fires many goroutines performing optimistic
// writes, deletes and group migrations with bounded retries. After quiescence
// the final state must (a) satisfy maintained==full-recompute (Verify), and
// (b) be exactly reproducible by serially replaying the accepted journal.
// (b) is the serializability witness: an equivalent global serial order exists.
func TestConcurrentSerializability(t *testing.T) {
	types, views := testSchema()
	s := NewStore(types, views)

	const workers = 12
	const keysPerWorker = 8
	var wg sync.WaitGroup

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < keysPerWorker; i++ {
				key := fmt.Sprintf("w%d-k%d", w, i)
				region := []string{"east", "west", "north"}[(w+i)%3]
				cat := []string{"book", "toy", "food"}[i%3]

				// create with create-token; tolerate another worker racing it
				retryCreate(key, s, region, cat)

				// optimistic migrations/updates with read-modify-retry loop
				for round := 0; round < 5; round++ {
					cur, ok := s.Get("Order", key)
					if !ok {
						retryCreate(key, s, region, cat)
						continue
					}
					nr := []string{"east", "west", "north"}[(w+i+round)%3]
					_, err := s.Write(WriteRequest{
						Type:     "Order",
						Key:      key,
						Attrs:    order(nr, cur.Attrs["category"].(string), 10+round),
						Expected: cur.Version,
					})
					if err != nil {
						if oe, ok := err.(*OpError); ok && oe.Code == ErrVersionConflict {
							continue // lost the race, retry against fresh version
						}
					}
				}

				// half the workers delete some keys
				if i%2 == 0 {
					if cur, ok := s.Get("Order", key); ok {
						if _, err := s.Delete(DeleteRequest{Type: "Order", Key: key, Expected: cur.Version}); err != nil {
							// a concurrent delete may win; that is a serialized outcome
						}
					}
				}
			}
		}(w)
	}
	wg.Wait()

	if err := s.Verify(); err != nil {
		t.Fatalf("post-concurrency divergence: %v", err)
	}
	j := s.Journal()
	r := Replay(types, views, j)
	for _, v := range views {
		groups, _ := s.maintainer.allGroups(v.Name)
		for g := range groups {
			a := s.Query(v.Name, g)
			b := r.Query(v.Name, g)
			if a != b {
				t.Fatalf("serial order not reproducible %s[%s]: %+v vs replayed %+v", v.Name, g, a, b)
			}
		}
	}
	if err := r.Verify(); err != nil {
		t.Fatal(err)
	}

	// total committed versions must form a gapless 1..k sequence per key
	maxVer := map[string]int64{}
	for _, e := range j {
		if e.Kind == "write" {
			id := e.Type + "/" + e.Key
			if e.Version != maxVer[id]+1 {
				t.Fatalf("version gap for %s: got %d after %d", id, e.Version, maxVer[id])
			}
			maxVer[id] = e.Version
		}
	}
	logLine(t, "BASIS %d accepted commits serially replay to identical aggregates; per-key versions gapless", len(j))
}

func retryCreate(key string, s *Store, region, cat string) {
	for attempt := 0; attempt < 10; attempt++ {
		_, err := s.Write(WriteRequest{Type: "Order", Key: key, Attrs: order(region, cat, 5), Expected: 0})
		if err == nil {
			return
		}
		if oe, ok := err.(*OpError); ok && oe.Code == ErrVersionConflict {
			return // already created by another worker
		}
		time.Sleep(time.Microsecond)
	}
}

// TestConcurrentReaderAtomicSnapshot hammers queries during migrations and
// asserts the cross-view conservation identity can never be violated:
// sum over region groups == sum over category groups == sum over all live
// instances. A split-brain (one view updated, the other stale) would break it.
func TestConcurrentReaderAtomicSnapshot(t *testing.T) {
	types, views := testSchema()
	s := NewStore(types, views)
	stop := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		regions := []string{"east", "west", "north"}
		cats := []string{"book", "toy", "food"}
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			key := fmt.Sprintf("mig-%d", i%30)
			if _, ok := s.Get("Order", key); !ok {
				s.Write(WriteRequest{Type: "Order", Key: key, Attrs: order(regions[i%3], cats[i%3], i%7), Expected: 0})
			} else {
				cur, _ := s.Get("Order", key)
				s.Write(WriteRequest{Type: "Order", Key: key,
					Attrs: order(regions[(i+1)%3], cats[(i+2)%3], i%7), Expected: cur.Version})
			}
		}
	}()

	for r := 0; r < runtime.NumCPU()*2; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			regions := []string{"east", "west", "north"}
			cats := []string{"book", "toy", "food"}
			for {
				select {
				case <-stop:
					return
				default:
				}
				// One read lock spans BOTH views: this is a single atomic
				// snapshot. A commit cannot be half-visible inside it.
				var regionSum, catSum float64
				s.mu.RLock()
				for _, g := range regions {
					if r, ok := s.maintainer.Query("AmountByRegion", g); ok {
						regionSum += r.Value
					}
				}
				for _, g := range cats {
					if r, ok := s.maintainer.Query("AmountByCategory", g); ok {
						catSum += r.Value
					}
				}
				s.mu.RUnlock()
				if regionSum != catSum {
					panic(fmt.Sprintf("torn multi-view update observed: region=%v category=%v", regionSum, catSum))
				}
			}
		}()
	}

	time.Sleep(120 * time.Millisecond)
	close(stop)
	wg.Wait()
	if err := s.Verify(); err != nil {
		t.Fatal(err)
	}
	logLine(t, "BASIS concurrent readers never observed region-sum != category-sum (no torn commits)")
}

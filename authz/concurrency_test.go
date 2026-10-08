package authz

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"
)

// Concurrency proof: many goroutines replace the whole set while others
// evaluate. Every evaluation result must equal the naive model run against
// the exact set recorded for the version the result carries, i.e. each
// evaluation observes one complete version and the concurrent execution is
// equivalent to some serial order. Run with -race.

func TestConcurrentReplaceAndEvaluate(t *testing.T) {
	store := newStore(t)

	var mu sync.Mutex
	history := map[uint64][]Policy{}
	record := func(v uint64, set []Policy) {
		mu.Lock()
		history[v] = append([]Policy(nil), set...)
		mu.Unlock()
	}
	lookup := func(v uint64) ([]Policy, bool) {
		// The store may already serve a version whose recording writer has
		// not published to this test-side history yet; wait briefly.
		for i := 0; i < 10000; i++ {
			mu.Lock()
			set, ok := history[v]
			mu.Unlock()
			if ok {
				return set, true
			}
			time.Sleep(time.Millisecond)
		}
		return nil, false
	}
	record(0, nil)

	const writers = 4
	const readers = 8
	const rounds = 100

	var wg sync.WaitGroup
	errCh := make(chan error, writers*rounds+readers*rounds*4)

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			for i := 0; i < rounds; i++ {
				n := 1 + r.Intn(8)
				set := make([]Policy, 0, n)
				for j := 0; j < n; j++ {
					p := randPolicy(r, j)
					p.Name = fmt.Sprintf("w%d-pol-%d", seed, j)
					set = append(set, p)
				}
				v, err := store.ReplaceAll(set)
				if err != nil {
					errCh <- fmt.Errorf("writer %d: %w", seed, err)
					return
				}
				record(v, set)
			}
		}(int64(w + 1))
	}

	for rd := 0; rd < readers; rd++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			for i := 0; i < rounds; i++ {
				req := randRequest(r)
				res, err := store.Evaluate(req)
				if err != nil {
					errCh <- fmt.Errorf("reader %d: %w", seed, err)
					return
				}
				set, ok := lookup(res.Version)
				if !ok {
					errCh <- fmt.Errorf("reader %d: result carries unknown version %d", seed, res.Version)
					return
				}
				model := &naiveModel{root: testRoot}
				model.version = res.Version
				model.policies = set
				want := model.eval(&req)
				match := resultsEqual(res, want)
				t.Logf("reader %d round %d: version=%d decision=%s match=%v", seed, i, res.Version, res.Decision, match)
				if !match {
					errCh <- fmt.Errorf("reader %d: version %d result %+v != model %+v", seed, res.Version, res, want)
					return
				}
			}
		}(int64(1000 + rd))
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
}

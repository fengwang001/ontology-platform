package meshauthz

import (
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

// TestConcurrentReplaceAndEvaluate hammers the store with concurrent
// whole-set replacements and evaluations. Every evaluation's evidence
// version must correspond to a completely installed set, and the
// decision must equal the naive model run on that exact version —
// i.e. concurrent results are equivalent to some serial order and no
// evaluation ever observes a half-installed set.
func TestConcurrentReplaceAndEvaluate(t *testing.T) {
	s := newTestStore(t)

	var mu sync.Mutex
	registry := map[uint64][]Policy{0: nil}

	const (
		replacers     = 4
		evaluators    = 8
		replacesEach  = 50
		evaluatesEach = 500
		logEveryNthOp = 100
	)

	var wg sync.WaitGroup
	errs := make(chan error, replacers+evaluators)

	// mu pairs "publish + register" and "evaluate + lookup" so the
	// test-side registry never lags the store; the store itself is
	// still contended by all goroutines.
	for w := 0; w < replacers; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(1000 + worker)))
			for i := 0; i < replacesEach; i++ {
				set := randPolicySet(rng)
				mu.Lock()
				v, err := s.ReplaceAll(set)
				if err != nil {
					mu.Unlock()
					errs <- err
					return
				}
				registry[v] = set
				mu.Unlock()
			}
		}(w)
	}

	for w := 0; w < evaluators; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(2000 + worker)))
			for i := 0; i < evaluatesEach; i++ {
				req := randRequest(rng)
				mu.Lock()
				res, err := s.Evaluate(req)
				if err != nil {
					mu.Unlock()
					errs <- err
					return
				}
				set, ok := registry[res.Evidence.Version]
				mu.Unlock()
				if !ok {
					errs <- &versionNotInstalledError{version: res.Evidence.Version}
					return
				}
				want := naiveEvaluate(set, res.Evidence.Version, testRootNS, req)
				if !reflect.DeepEqual(normalizeResult(res), normalizeResult(want)) {
					t.Errorf("worker %d op %d: version %d decision %s, naive model says %s",
						worker, i, res.Evidence.Version, res.Decision, want.Decision)
					return
				}
				if i%logEveryNthOp == 0 {
					t.Logf("worker %d op %d: version=%d decision=%s consistent-with-model=true",
						worker, i, res.Evidence.Version, res.Decision)
				}
			}
		}(w)
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent op failed: %v", err)
	}

	mu.Lock()
	installed := len(registry)
	mu.Unlock()
	t.Logf("installed versions=%d final store version=%d", installed, s.Version())
	if s.Version() != uint64(replacers*replacesEach) {
		t.Fatalf("version = %d, want %d (monotonic +1 per replace)", s.Version(), replacers*replacesEach)
	}
}

type versionNotInstalledError struct{ version uint64 }

func (e *versionNotInstalledError) Error() string {
	return "evaluation observed a version that was never fully installed"
}

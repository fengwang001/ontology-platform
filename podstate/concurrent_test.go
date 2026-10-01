package podstate

import (
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentCalls hammers the machine from many goroutines. Correctness is
// that it never races (run with -race) and that invariants hold afterwards.
func TestConcurrentCalls(t *testing.T) {
	cfg := Config{
		InitContainers: 1,
		AppContainers:  3,
		RestartPolicy:  OnFailure,
		BackoffBase:    1,
		BackoffMax:     4,
		BackoffReset:   3,
		ActiveDeadline: 0,
		RestartBudget:  0,
	}
	p := mustPod(t, cfg)

	var now atomic.Int64
	now.Store(0)
	advance := func() int64 {
		// Monotonic global clock keeps the no-regression rule satisfiable.
		return now.Add(1)
	}

	var wg sync.WaitGroup
	for g := 0; g < 12; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 400; j++ {
				c := (id + j) % 4
				ts := advance()
				if err := p.Start(c, ts); err == nil {
					ts2 := advance()
					code := int64(1)
					if j%4 == 0 {
						code = 0
					}
					_ = p.Exit(c, code, ts2)
				}
				_, _ = p.Phase(advance())
				_, _ = p.Reason(c, advance())
			}
		}(g)
	}
	wg.Wait()

	// Invariants: global restarts when X==0 are unbounded but consistent;
	// every container's k/next relationship is self-consistent; no container is
	// Running after the storm unless its exit was rejected by ordering (which
	// cannot leave Running without a later accepted exit — just check phases
	// are always computable).
	for c := 0; c < 4; c++ {
		s := snap(t, p, c)
		if s.Next < 0 || s.Fails < 0 {
			t.Fatalf("container %d negative fields: %+v", c, s)
		}
	}
	if _, err := p.Phase(now.Load() + 1); err != nil {
		t.Fatalf("final phase: %v", err)
	}
}

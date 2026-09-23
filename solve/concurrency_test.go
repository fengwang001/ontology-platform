package solve_test

import (
	"sync"
	"testing"

	"ontology/solve"
)

// TestConcurrentSolve runs M goroutines solving the same roots on one
// shared Solver/Graph. All results must be byte-identical and valid;
// run with -race to prove no shared state beyond the attempts counter
// is mutated. Synchronization uses WaitGroup/channels only, no sleeps.
func TestConcurrentSolve(t *testing.T) {
	g := buildGraph(t,
		map[string][]string{
			"a": {"1.0.0", "2.0.0"},
			"b": {"1.0.0", "1.5.0", "2.0.0"},
			"c": {"0.1.0", "0.2.0"},
		},
		[]dep{
			{"a", "2.0.0", "b", ">=1.5.0"},
			{"a", "1.0.0", "b", "<1.5.0"},
			{"b", "2.0.0", "c", ">=0.2.0"},
			{"b", "1.5.0", "c", "<0.2.0"},
			{"b", "1.0.0", "c", "<0.2.0"},
		},
	)
	roots := []solve.Requirement{{Package: "a", Constraint: ">=1.0.0"}}
	s := solve.New(g)
	want, err := s.Solve(roots)
	if err != nil {
		t.Fatal(err)
	}
	const m = 32
	results := make(chan string, m)
	errs := make(chan error, m)
	var wg sync.WaitGroup
	for i := 0; i < m; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sol, err := s.Solve(roots)
			if err != nil {
				errs <- err
				return
			}
			if err := solve.Validate(g, roots, sol); err != nil {
				errs <- err
				return
			}
			results <- sol.String()
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent solve: %v", err)
	}
	n := 0
	for got := range results {
		n++
		if got != want.String() {
			t.Fatalf("result differs:\n%s\nwant:\n%s", got, want)
		}
	}
	if n != m {
		t.Fatalf("got %d results, want %d", n, m)
	}
	t.Logf("attempts=%d across %d solves", s.Attempts(), m+1)
}

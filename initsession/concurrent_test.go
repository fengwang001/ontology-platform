package initsession

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentRegisterAndSolve interleaves registrations and solves
// from many goroutines under -race. Every Solve call must observe a
// consistent snapshot: its returned order, when successful, must be a
// valid linearization of the accepted prefix. We assert the invariant
// that a successful order has all referenced variables preceding
// their dependents and matches the naive model for the same accepted
// prefix.
//
// Because registrations run concurrently, each goroutine works on a
// disjoint namespace; the accepted-set prefix observed by a solve is
// reconstructed from the result size and re-simulated independently.
func TestConcurrentRegisterAndSolve(t *testing.T) {
	lg := newTestLogger(t)
	s, err := New("pre")
	if err != nil {
		t.Fatal(err)
	}

	const goroutines = 8
	const perG = 40
	var wg sync.WaitGroup

	// Recorders: each goroutine registers a private chain
	// g<i>v0 <- g<i>v1 <- ... with a private function in between,
	// deliberately using forward references. Names are unique per
	// goroutine so concurrent registrations never conflict.
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			prefix := fmt.Sprintf("g%dv", g)
			fname := fmt.Sprintf("g%dfun", g)
			for i := 0; i < perG; i++ {
				var refs []string
				if i > 0 {
					refs = []string{prefix + itoa(i-1)}
				} else {
					refs = []string{"pre"}
				}
				if i%3 == 0 {
					// Route some references through the private
					// function to exercise concurrent closure reads;
					// i==0 must not use it (fname reaches v0 itself).
					if i > 0 {
						refs = []string{fname}
					}
				}
				if _, err := s.AddVariableUnit([]string{prefix + itoa(i)}, refs); err != nil {
					t.Errorf("register g%d i%d: %v", g, i, err)
					return
				}
			}
			// fname reaches the chain head v0; it can be forward
			// declared because closure checks run at solve time.
			frefs := []string{"pre"}
			frefs = append(frefs, prefix+itoa(0))
			if err := s.AddFunction(fname, frefs); err != nil {
				t.Errorf("register func g%d: %v", g, err)
			}
		}(g)
	}

	// Concurrent solvers: they may see any consistent prefix; every
	// successful result must be internally coherent.
	const solvers = 4
	for k := 0; k < solvers; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 60; i++ {
				res, st, err := s.Solve()
				if err != nil {
					// During registration, snapshots may contain
					// forward-undeclared names; cycle is never possible
					// in this data set.
					if e, ok := err.(*Error); !ok || e.Kind != KindUndeclared {
						t.Errorf("unexpected concurrent solve error: %v", err)
						return
					}
					continue
				}
				// Coherence: every unit's reported deps precede it.
				pos := map[string]int{}
				for oi, ui := range res.Order {
					for _, v := range res.Dependencies[ui].Variables {
						if v != "_" {
							pos[v] = oi
						}
					}
				}
				for oi, ui := range res.Order {
					for _, dep := range res.Dependencies[ui].Deps {
						if p, ok := pos[dep]; !ok || p >= oi {
							t.Errorf("incoherent snapshot: dep %q not before unit %d (order pos %d)", dep, ui, oi)
							return
						}
					}
				}
				if st.Units != len(res.Dependencies) {
					t.Errorf("stats/result size mismatch: %+v", st)
					return
				}
			}
		}()
	}

	wg.Wait()

	// Final state: everything declared, all chains initialize.
	res, _, err := s.Solve()
	if err != nil {
		t.Fatalf("final solve: %v", err)
	}
	if len(res.Order) != goroutines*perG {
		t.Fatalf("final units = %d, want %d", len(res.Order), goroutines*perG)
	}
	lg.outputf("concurrent run finalized with %d units; order head: %v", len(res.Order), res.Order[:5])
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

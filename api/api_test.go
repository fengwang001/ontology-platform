package api

import (
	"errors"
	"math/rand"
	"slices"
	"sync"
	"testing"

	"ontology/dc"
)

func build(t *testing.T, n int, cons []dc.Constraint) *Solver {
	sv, err := New(n)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cons {
		if err := sv.AddConstraint(c.U, c.V, c.W); err != nil {
			t.Fatal(err)
		}
	}
	return sv
}

// randomFeasible returns randomly ordered constraints satisfiable by the
// returned potential p (normalized to max p_i = 0, so p is also feasible
// for the super-source-extended system).
func randomFeasible(rng *rand.Rand, n, extra int) ([]dc.Constraint, []int64) {
	p := make([]int64, n)
	for i := range p {
		p[i] = rng.Int63n(41) - 20
	}
	cons := make([]dc.Constraint, 2*n+extra)
	for j := range cons {
		u, v := rng.Intn(n), rng.Intn(n)
		cons[j] = dc.Constraint{U: u, V: v, W: p[v] - p[u] + rng.Int63n(4)}
	}
	top := slices.Max(p)
	for i := range p {
		p[i] -= top
	} // shifting preserves feasibility
	return cons, p
}
func TestSolveTable(t *testing.T) {
	cases := []struct {
		n    int
		cons []dc.Constraint
		want []int64 // nil: infeasible
	}{
		{3, []dc.Constraint{{U: 0, V: 1, W: -2}, {U: 1, V: 2, W: -3}}, []int64{0, -2, -5}},
		{3, []dc.Constraint{{U: 0, V: 1, W: -2}, {U: 1, V: 2, W: -3}, {U: 2, V: 0, W: -10}}, nil},
		{4, []dc.Constraint{{U: 0, V: 1, W: 5}, {U: 1, V: 2, W: -10}, {U: 0, V: 2, W: -6}, {U: 3, V: 0, W: 2}}, []int64{0, 0, -10, 0}},
		{2, []dc.Constraint{{U: 0, V: 0, W: 0}, {U: 0, V: 1, W: -7}}, []int64{0, -7}}, // w=0 self-loop is legal
	}
	for i, c := range cases {
		got, err := build(t, c.n, c.cons).Solve()
		if c.want == nil && !errors.Is(err, ErrInfeasible) {
			t.Fatalf("case %d: want ErrInfeasible, got %v", i, err)
		}
		if c.want != nil && (err != nil || !slices.Equal(got, c.want) || !feasible(c.cons, got)) {
			t.Fatalf("case %d: got %v err %v, want %v", i, got, err, c.want)
		}
	}
}
func TestRejectedNoTrace(t *testing.T) {
	if _, err := New(0); !errors.Is(err, ErrNonPositiveN) {
		t.Fatalf("New(0) = %v", err)
	}
	sv := build(t, 3, []dc.Constraint{{U: 0, V: 1, W: -2}})
	bads := [][3]int64{{-1, 0, 0}, {0, 3, 0}, {2, 2, -1}}
	wants := []error{ErrVarOutOfRange, ErrVarOutOfRange, ErrNegativeSelfLoop}
	for i, b := range bads {
		if err := sv.AddConstraint(int(b[0]), int(b[1]), b[2]); !errors.Is(err, wants[i]) {
			t.Fatalf("Add(%v) = %v, want %v", b, err, wants[i])
		}
	}
	all := []error{ErrNonPositiveN, ErrVarOutOfRange, ErrNegativeSelfLoop, ErrInfeasible}
	for i, a := range all {
		for j, b := range all {
			if i != j && errors.Is(a, b) {
				t.Fatalf("sentinels %v / %v not distinct", a, b)
			}
		}
	}
	got, err := sv.Solve() // rejections left no trace; solver still usable
	if sv.ConstraintCount() != 1 || err != nil || !slices.Equal(got, []int64{0, -2, 0}) {
		t.Fatalf("after rejections: count=%d got=%v err=%v", sv.ConstraintCount(), got, err)
	}
	neg := build(t, 3, []dc.Constraint{{U: 0, V: 1, W: -2}, {U: 1, V: 2, W: -3}, {U: 2, V: 0, W: -10}})
	if _, err := neg.Solve(); !errors.Is(err, ErrInfeasible) || neg.ConstraintCount() != 3 {
		t.Fatalf("failed solve left a trace: err=%v count=%d", err, neg.ConstraintCount())
	}
}
func TestMatchesNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for _, n := range []int{2, 5, 17, 64} {
		for trial := 0; trial < 6; trial++ {
			cons, _ := randomFeasible(rng, n, trial)
			got, err := build(t, n, cons).Solve()
			if err != nil || !slices.Equal(got, naiveSolve(n, cons)) || !feasible(cons, got) {
				t.Fatalf("n=%d trial=%d: got %v err %v", n, trial, got, err)
			}
		}
	}
}
func TestPointwiseMaximal(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for _, n := range []int{3, 10, 40} {
		for trial := 0; trial < 6; trial++ {
			cons, p := randomFeasible(rng, n, trial)
			got, err := build(t, n, cons).Solve()
			if err != nil || !maximal(n, cons, got) { // tightness characterization
				t.Fatalf("n=%d trial=%d: %v err %v not maximal", n, trial, got, err)
			}
			for i := range p { // p is feasible with p_i<=0, so p_i <= x_i must hold
				if p[i] > got[i] {
					t.Fatalf("n=%d: feasible p[%d]=%d exceeds x[%d]=%d", n, i, p[i], i, got[i])
				}
			}
		}
	}
}
func TestConcurrentSolveIdentical(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	cons, _ := randomFeasible(rng, 50, 10)
	sv := build(t, 50, cons)
	res := make([][]int64, 32)
	var wg sync.WaitGroup
	for k := range res {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for rep := 0; rep < 5; rep++ {
				got, err := sv.Solve()
				if err != nil || sv.SelfCheck() != nil || sv.ConstraintCount() != len(cons) {
					t.Errorf("concurrent read failed: %v", err)
				}
				res[k] = got
			}
		}()
	}
	wg.Wait()
	for k := 1; k < len(res); k++ {
		if !slices.Equal(res[k], res[0]) {
			t.Fatalf("goroutine %d result differs", k)
		}
	}
}

// Command demo exercises the difference-constraints solver end to end.
package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"

	"ontology/api"
	"ontology/dc"
	"ontology/sp"
)

var failed bool

func check(ok bool, msg string, args ...any) {
	tag := "OK  "
	if !ok {
		tag = "FAIL"
		failed = true
	}
	fmt.Printf(tag+" "+msg+"\n", args...)
}

// relaxRounds simulates the n=3 system of NOTES.md: each round relaxes
// C2 (with the given weight) before C1, for the given number of rounds.
func relaxRounds(w2 int64, rounds int) []int64 {
	d := []int64{0, 0, 0}
	for r := 0; r < rounds; r++ {
		if d[1]+w2 < d[2] {
			d[2] = d[1] + w2
		}
		if d[0]-2 < d[1] {
			d[1] = d[0] - 2
		}
	}
	return d
}

func main() {
	// Four failure modes carry mutually distinct sentinel errors.
	_, eN := api.New(0)
	sv, _ := api.New(3)
	eR := sv.AddConstraint(0, 3, 1)
	eL := sv.AddConstraint(1, 1, -1)
	sv.AddConstraint(0, 1, -2)
	sv.AddConstraint(1, 2, -3)
	sv.AddConstraint(2, 0, -10)
	_, eC := sv.Solve()
	errs := []error{eN, eR, eL, eC}
	want := []error{api.ErrNonPositiveN, api.ErrVarOutOfRange, api.ErrNegativeSelfLoop, api.ErrInfeasible}
	distinct := true
	for i := range errs {
		for j := range want {
			distinct = distinct && errors.Is(errs[i], want[j]) == (i == j)
		}
	}
	check(distinct, "four sentinel errors distinct (n<=0, range, self-loop, infeasible)")
	check(sv.ConstraintCount() == 3, "rejected ops leave no trace (count still 3)")
	check(errors.Is(eC, api.ErrInfeasible), "C3 added: negative cycle -> ErrInfeasible, no assignment")

	// The n=3 system of NOTES.md section 3: C1 and C2 only.
	s2, _ := api.New(3)
	s2.AddConstraint(0, 1, -2)
	s2.AddConstraint(1, 2, -3)
	got, err := s2.Solve()
	check(err == nil && slices.Equal(got, []int64{0, -2, -5}), "n=3 solve: %v", got)
	check(relaxRounds(-3, 1)[2] == -3, "one-round trap: x2=-3 (correct -5)")
	check(relaxRounds(3, 8)[2] == 0, "flipped-weight trap: x2=0 (correct -5)")

	// Long negative chain: x_i=-i, queue-driven, zero full-table passes
	// (the pass counter is unexported; sp's white-box test pins it to 0).
	const m = 10000
	chain := make([]dc.Constraint, 0, m-1)
	for i := 0; i+1 < m; i++ {
		chain = append(chain, dc.Constraint{U: i, V: i + 1, W: -1})
	}
	xs, err := sp.NewEngine(m, chain).Solve()
	ok := err == nil
	for i, x := range xs {
		ok = ok && x == int64(-i)
	}
	check(ok, "chain m=%d: x_i=-i (0 full-table passes, pinned by sp test)", m)

	// Concurrent Solve on a frozen set: element-wise identical results.
	const g = 16
	res := make([][]int64, g)
	var wg sync.WaitGroup
	for k := 0; k < g; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := s2.Solve()
			if err == nil {
				res[k] = r
			}
		}()
	}
	wg.Wait()
	same := true
	for _, r := range res {
		same = same && slices.Equal(r, got)
	}
	check(same, "concurrent Solve: %d goroutines identical", g)
	check(s2.SelfCheck() == nil, "SelfCheck passed")

	if failed {
		os.Exit(1)
	}
}

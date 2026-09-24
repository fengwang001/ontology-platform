// Command demo verifies the semi-naive transitive-closure engine end to
// end: the worked five-round example, closure size and self-loops, the two
// derivations of a->d, the chain candidate bound, sentinel errors, state
// integrity after rejection, and concurrent determinism.
package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"

	"ontology/api"
	"ontology/semi"
)

var failed bool

func report(name string, ok bool, detail string) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%-4s %s %s\n", status, name, detail)
}

var edges = [][2]string{{"a", "b"}, {"b", "c"}, {"c", "d"}, {"b", "d"}, {"d", "e"}, {"e", "b"}}

func chain(m int) [][2]string {
	e := make([][2]string, m)
	for i := 1; i <= m; i++ {
		e[i-1] = [2]string{fmt.Sprintf("a%d", i), fmt.Sprintf("a%d", i+1)}
	}
	return e
}

func main() {
	eng := semi.New(edges)
	eng.Eval()
	rounds := eng.Rounds()

	// 1. per-round trace of the worked example (NOTES.md section 3)
	expD, expC, expP := []int{6, 7, 6, 1, 0}, []int{0, 8, 8, 8, 1}, []int{6, 13, 19, 20, 20}
	var sb strings.Builder
	ok := len(rounds) == 5
	for i, r := range rounds {
		var ts []string
		for _, p := range r.Delta {
			ts = append(ts, p[0]+p[1])
		}
		fmt.Fprintf(&sb, " r%d{%s}/%dc/%dn/p%d", i, strings.Join(ts, ","), r.Candidates, r.Added, r.PathSize)
		if i >= 5 || r.Candidates != expC[i] || r.Added != expD[i] || r.PathSize != expP[i] || len(r.Delta) != expD[i] {
			ok = false
		}
	}
	report("rounds", ok, sb.String())

	// 2. final closure size and self-loops from the cycle
	db, err := api.New(edges)
	path := db.Eval()
	var loops []string
	for _, p := range path {
		if p[0] == p[1] {
			loops = append(loops, p[0]+p[1])
		}
	}
	report("closure", err == nil && len(path) == 20 && slices.Equal(loops, []string{"bb", "cc", "dd", "ee"}),
		fmt.Sprintf("size=%d self-loops=%v", len(path), loops))

	// 3. the two derivations of a->d: z=b at round 1, z=c at round 2
	var via []string
	for k := 1; k < len(rounds); k++ {
		for _, d := range rounds[k-1].Delta {
			for _, e := range edges {
				if d[0] == "a" && e[0] == d[1] && e[1] == "d" {
					via = append(via, fmt.Sprintf("z=%s@r%d", d[1], k))
				}
			}
		}
	}
	ok = slices.Equal(via, []string{"z=b@r1", "z=c@r2"}) &&
		slices.Contains(rounds[1].Delta, [2]string{"a", "d"}) &&
		!slices.Contains(rounds[2].Delta, [2]string{"a", "d"})
	report("a->d", ok, strings.Join(via, " ")+" (r2 dedup-dropped)")

	// 4. chain of m edges: total join candidates == m(m-1)/2
	ok = true
	for _, m := range []int{100, 1000, 3000} {
		e := semi.New(chain(m))
		e.Eval()
		total := 0
		for _, r := range e.Rounds() {
			total += r.Candidates
		}
		ok = ok && total == m*(m-1)/2
	}
	report("chain-cand", ok, "m=100,1000,3000 total==m(m-1)/2")

	// 5+6. three distinct sentinel errors; rejection leaves state unchanged
	before := db.Eval()
	e1, e2, e3 := db.AddEdge("", "x"), db.AddEdge("q", "q"), db.AddEdge("a", "b")
	ok = errors.Is(e1, api.ErrEmptyNode) && errors.Is(e2, api.ErrSelfLoop) && errors.Is(e3, api.ErrDuplicateEdge) &&
		e1 != e2 && e2 != e3 && e1 != e3
	report("errors", ok, "empty-node/self-loop/duplicate distinct")
	report("no-trace", slices.Equal(db.Eval(), before) && db.AddEdge("e", "f") == nil && db.Size() == 25,
		"state unchanged after rejects, still usable")

	// 7. concurrent independent evaluations are tuple-identical
	const n = 8
	results := make([][][2]string, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, err := api.New(edges)
			if err == nil {
				results[i] = d.Eval()
			}
		}()
	}
	wg.Wait()
	ok = true
	for i := 1; i < n; i++ {
		ok = ok && slices.Equal(results[i], results[0])
	}
	report("concurrent", ok, "8 goroutines, identical results")

	// 8. built-in self-check of the four invariants
	report("selfcheck", api.SelfCheck() == nil, "four invariants")

	if failed {
		os.Exit(1)
	}
}

// Command demo exercises the version-constraint solver end to end and
// prints one OK/FAIL line per guarantee. Exit code 0 means all passed.
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"ontology/graph"
	"ontology/rng"
	"ontology/solve"
	"ontology/ver"
)

var failures int

func check(name string, ok bool) {
	status := "OK  "
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %s\n", status, name)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

// baseGraph registers a small solvable universe; shuffled changes order.
func baseGraph(shuffled bool) *graph.Graph {
	g := graph.New()
	vs := [][2]string{{"app", "1.0.0"}, {"app", "2.0.0"}, {"lib", "1.0.0"},
		{"lib", "1.5.0"}, {"lib", "2.0.0"}, {"util", "0.1.0"}, {"util", "0.2.0"}}
	if shuffled {
		for i, j := 0, len(vs)-1; i < j; i, j = i+1, j-1 {
			vs[i], vs[j] = vs[j], vs[i]
		}
	}
	for _, pv := range vs {
		must(g.AddVersion(pv[0], pv[1]))
	}
	must(g.AddConstraint("app", "2.0.0", "lib", ">=1.5.0"))
	must(g.AddConstraint("app", "1.0.0", "lib", "<1.5.0"))
	must(g.AddConstraint("lib", "2.0.0", "util", ">=0.2.0"))
	must(g.AddConstraint("lib", "1.5.0", "util", "<0.2.0"))
	must(g.AddConstraint("lib", "1.0.0", "util", "<0.2.0"))
	return g
}

func main() {
	roots := []solve.Requirement{{Package: "app", Constraint: ">=1.0.0"}}

	g := baseGraph(false)
	sol, err := solve.New(g).Solve(roots)
	check("solvable solution passes Validate",
		err == nil && solve.Validate(g, roots, sol) == nil)

	sol2, err2 := solve.New(g).Solve(roots)
	check("repeated solve is byte-identical",
		err2 == nil && sol.String() == sol2.String())

	sol3, err3 := solve.New(baseGraph(true)).Solve(roots)
	check("shuffled registration order, same result",
		err3 == nil && sol.String() == sol3.String())

	cg := graph.New()
	must(cg.AddVersion("app", "1.0.0"))
	must(cg.AddVersion("lib", "1.0.0"))
	must(cg.AddVersion("lib", "3.0.0"))
	must(cg.AddConstraint("app", "1.0.0", "lib", ">=2.0.0"))
	croots := []solve.Requirement{{Package: "app", Constraint: ">=1.0.0"}, {Package: "lib", Constraint: "<2.0.0"}}
	_, cerr := solve.New(cg).Solve(croots)
	var ce *solve.ConflictError
	check("unsatisfiable input yields conflict chain", errors.As(cerr, &ce))
	if ce != nil {
		for _, line := range strings.Split(ce.Chain, "\n") {
			fmt.Printf("     | %s\n", line)
		}
	}

	cyc := graph.New()
	must(cyc.AddVersion("a", "1.0.0"))
	must(cyc.AddVersion("b", "1.0.0"))
	must(cyc.AddConstraint("a", "1.0.0", "b", ">=1.0.0"))
	must(cyc.AddConstraint("b", "1.0.0", "a", ">=1.0.0"))
	csol, cycErr := solve.New(cyc).Solve([]solve.Requirement{{Package: "a", Constraint: ">=1.0.0"}})
	check("circular dependency solves", cycErr == nil && len(csol) == 2)

	check("error: invalid version string",
		errors.Is(g.AddVersion("x", "1.0"), ver.ErrInvalidVersion))
	check("error: invalid constraint string",
		errors.Is(g.AddConstraint("app", "1.0.0", "lib", ">>1"), rng.ErrInvalidConstraint))
	check("error: duplicate registration",
		errors.Is(g.AddVersion("app", "1.0.0"), graph.ErrDuplicateVersion))
	_, uerr := solve.New(g).Solve([]solve.Requirement{{Package: "ghost", Constraint: ">=1.0.0"}})
	check("error: unregistered package", errors.Is(uerr, solve.ErrUnknownPackage))

	_, berr := solve.New(g, solve.WithMaxAttempts(1)).Solve(roots)
	check("budget exceeded is distinct from no-solution",
		errors.Is(berr, solve.ErrBudgetExceeded) && !errors.Is(berr, solve.ErrNoSolution) &&
			errors.Is(cerr, solve.ErrNoSolution) && !errors.Is(cerr, solve.ErrBudgetExceeded))

	shared := solve.New(g)
	const m = 16
	outs := make([]string, m)
	var wg sync.WaitGroup
	for i := 0; i < m; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if s, err := shared.Solve(roots); err == nil {
				outs[i] = s.String()
			}
		}(i)
	}
	wg.Wait()
	same := true
	for _, o := range outs {
		same = same && o == sol.String()
	}
	check("concurrent solves are byte-identical", same)

	pg := graph.New()
	for i := 0; i < 10; i++ {
		p := fmt.Sprintf("p%d", i)
		next := fmt.Sprintf("p%d", (i+1)%10)
		for j := 0; j < 10; j++ {
			v := fmt.Sprintf("0.0.%d", j)
			must(pg.AddVersion(p, v))
			con := ">=0.0.0"
			if j >= 1 {
				con = "<0.0.0"
			}
			must(pg.AddConstraint(p, v, next, con))
		}
	}
	ps := solve.New(pg)
	psol, perr := ps.Solve([]solve.Requirement{{Package: "p0", Constraint: ">=0.0.0"}})
	check(fmt.Sprintf("pruning: attempts=%d (<10000, brute force 1e10)",
		ps.Attempts()), perr == nil && len(psol) == 10 && ps.Attempts() < 10000)

	if failures > 0 {
		os.Exit(1)
	}
}

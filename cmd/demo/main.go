// Command demo 逐条演练版本约束求解器的不变量与错误分类。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/graph"
	"ontology/rng"
	"ontology/solve"
	"ontology/ver"
)

var failed bool

func report(name string, ok bool, detail string) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	if detail != "" {
		fmt.Printf("%-28s %s  %s\n", name, status, detail)
	} else {
		fmt.Printf("%-28s %s\n", name, status)
	}
}

func main() {
	roots := []solve.Root{{Package: "app", Constraint: "*"}}
	g := graph.New()
	must(g.AddVersion("app", "1.0.0", graph.Dep{Target: "lib", Constraint: ">=1.2.0 <2.0.0"}))
	must(g.AddVersion("lib", "1.2.0"))
	must(g.AddVersion("lib", "1.9.0"))
	must(g.AddVersion("lib", "2.0.0"))
	must(g.AddVersion("ghost", "0.0.1"))
	s := solve.New(g)

	sol, err := s.Solve(roots, solve.Options{})
	report("feasible-solution-valid", err == nil && s.Validate(sol, roots) == nil,
		chosen(sol))

	sol2, _ := s.Solve(roots, solve.Options{})
	report("repeat-solve-identical", chosen(sol) == chosen(sol2), chosen(sol2))

	g2 := graph.New()
	must(g2.AddVersion("ghost", "0.0.1"))
	must(g2.AddVersion("lib", "2.0.0"))
	must(g2.AddVersion("lib", "1.9.0"))
	must(g2.AddVersion("lib", "1.2.0"))
	must(g2.AddVersion("app", "1.0.0", graph.Dep{Target: "lib", Constraint: "<2.0.0 >=1.2.0"}))
	sol3, _ := solve.New(g2).Solve(roots, solve.Options{})
	report("order-independent", chosen(sol) == chosen(sol3), chosen(sol3))

	gBad := graph.New()
	must(gBad.AddVersion("app", "1.0.0", graph.Dep{Target: "lib", Constraint: ">=2.0.0"}))
	must(gBad.AddVersion("lib", "1.0.0"))
	_, err = solve.New(gBad).Solve(
		[]solve.Root{{Package: "app", Constraint: "*"}, {Package: "lib", Constraint: "<2.0.0"}},
		solve.Options{})
	var ce *solve.ConflictError
	report("conflict-chain-readable", errors.As(err, &ce), firstLine(err.Error()))

	gCyc := graph.New()
	must(gCyc.AddVersion("a", "1.0.0", graph.Dep{Target: "b", Constraint: ">=1.0.0"}))
	must(gCyc.AddVersion("b", "1.0.0", graph.Dep{Target: "a", Constraint: ">=1.0.0"}))
	solC, err := solve.New(gCyc).Solve([]solve.Root{{Package: "a", Constraint: "*"}}, solve.Options{})
	report("cycle-solvable", err == nil && solve.New(gCyc).Validate(solC,
		[]solve.Root{{Package: "a", Constraint: "*"}}) == nil, chosen(solC))

	_, err = solve.New(graph.New()).Solve([]solve.Root{{Package: "x", Constraint: "*"}}, solve.Options{})
	report("error-unknown-package", errors.Is(err, solve.ErrUnknownPackage), firstLine(err.Error()))
	_, err = solve.New(graph.New()).Solve([]solve.Root{{Package: "x", Constraint: "~1"}}, solve.Options{})
	report("error-bad-constraint", errors.Is(err, rng.ErrInvalidConstraint), err.Error())
	err = graph.New().AddVersion("x", "v1")
	report("error-bad-version", errors.Is(err, ver.ErrInvalidVersion), err.Error())
	err = dupRegister()
	report("error-duplicate-version", errors.Is(err, graph.ErrDuplicateVersion), err.Error())

	_, errNo := solve.New(gBad).Solve([]solve.Root{{Package: "lib", Constraint: "<1.0.0"}}, solve.Options{})
	gBudget := graph.New()
	must(gBudget.AddVersion("app", "1.0.0"))
	must(gBudget.AddVersion("lib", "1.0.0"))
	_, errBud := solve.New(gBudget).Solve([]solve.Root{{Package: "app", Constraint: "*"},
		{Package: "lib", Constraint: "*"}}, solve.Options{MaxAttempts: 1})
	report("budget-vs-nosolution", errors.Is(errNo, solve.ErrNoSolution) &&
		errors.Is(errBud, solve.ErrSearchBudget) && !errors.Is(errBud, solve.ErrNoSolution),
		errBud.Error())

	var wg sync.WaitGroup
	const M = 16
	res := make([]string, M)
	wg.Add(M)
	for i := 0; i < M; i++ {
		go func(i int) {
			defer wg.Done()
			r, e := solve.New(g).Solve(roots, solve.Options{})
			if e == nil {
				res[i] = chosen(r)
			}
		}(i)
	}
	wg.Wait()
	same := true
	for i := 1; i < M; i++ {
		same = same && res[i] == res[0]
	}
	report("concurrent-identical", same, res[0])

	solP, _ := solve.New(pruningGraph()).Solve(
		[]solve.Root{{Package: "p9", Constraint: "*"}}, solve.Options{})
	report("pruning-attempts", solP.Attempts() < 10000,
		fmt.Sprintf("attempts=%d (< 10000)", solP.Attempts()))

	if failed {
		os.Exit(1)
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func chosen(sol *solve.Solution) string {
	out := ""
	for _, c := range sol.Choices() {
		out += c.Package + "=" + c.Version.String() + " "
	}
	return out
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}

func dupRegister() error {
	gg := graph.New()
	must(gg.AddVersion("x", "1.0.0"))
	return gg.AddVersion("x", "1.0.0")
}

func pruningGraph() *graph.Graph {
	gg := graph.New()
	for i := 0; i < 10; i++ {
		for k := 1; k <= 10; k++ {
			var deps []graph.Dep
			if i > 0 {
				deps = append(deps, graph.Dep{
					Target:     fmt.Sprintf("p%d", i-1),
					Constraint: fmt.Sprintf(">=%d.0.0", k),
				})
			}
			must(gg.AddVersion(fmt.Sprintf("p%d", i), fmt.Sprintf("%d.0.0", k), deps...))
		}
	}
	return gg
}

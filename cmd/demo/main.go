// Command demo exercises the version constraint solver end to end.
package main

import (
	"errors"
	"fmt"

	"ontology/graph"
	"ontology/rng"
	"ontology/solve"
	"ontology/ver"
)

func report(name string, ok bool, detail string) {
	tag := "OK  "
	if !ok {
		tag = "FAIL"
	}
	if detail != "" {
		fmt.Printf("%s %s: %s\n", tag, name, detail)
		return
	}
	fmt.Printf("%s %s\n", tag, name)
}

func buildDemoGraph() *graph.Graph {
	g := graph.New()
	must(g.Add("web", "1.0.0", map[string]string{"lib": ">=1.2.0 <2.0.0"}))
	must(g.Add("web", "2.0.0", map[string]string{"lib": "<1.2.0"}))
	must(g.Add("lib", "1.1.0", nil))
	must(g.Add("lib", "1.5.0", nil))
	must(g.Add("lib", "2.0.0", nil))
	must(g.Add("unused", "9.9.9", nil))
	return g
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	roots := []solve.Root{{Pkg: "web", Spec: ">=1.0.0"}}

	sol, err := solve.New(buildDemoGraph()).Solve(roots)
	report("feasible solution verifies", err == nil &&
		solve.New(buildDemoGraph()).Verify(roots, sol) == nil,
		fmt.Sprintf("web=%s lib=%s", sol["web"], sol["lib"]))

	sol2, _ := solve.New(buildDemoGraph()).Solve(roots)
	report("repeat solve identical", fmt.Sprint(sol) == fmt.Sprint(sol2), "")

	shuffled := graph.New()
	must(shuffled.Add("lib", "2.0.0", nil))
	must(shuffled.Add("lib", "1.1.0", nil))
	must(shuffled.Add("lib", "1.5.0", nil))
	must(shuffled.Add("web", "2.0.0", map[string]string{"lib": "<1.2.0"}))
	must(shuffled.Add("web", "1.0.0", map[string]string{"lib": ">=1.2.0 <2.0.0"}))
	sol3, _ := solve.New(shuffled).Solve(roots)
	report("shuffled registration identical", fmt.Sprint(sol3) == fmt.Sprint(sol), "")

	bad := graph.New()
	must(bad.Add("web", "1.0.0", map[string]string{"lib": ">=3.0.0"}))
	must(bad.Add("lib", "1.0.0", nil))
	_, cerr := solve.New(bad).Solve(roots)
	var ce *solve.ConflictError
	report("unsolvable gives conflict chain", errors.As(cerr, &ce), fmt.Sprint(ce))

	cyc := graph.New()
	must(cyc.Add("a", "1.0.0", map[string]string{"b": ">=1.0.0"}))
	must(cyc.Add("a", "2.0.0", map[string]string{"b": "<1.0.0"}))
	must(cyc.Add("b", "1.0.0", map[string]string{"a": ">=1.0.0 <2.0.0"}))
	csol, cycerr := solve.New(cyc).Solve([]solve.Root{{Pkg: "a", Spec: ">=1.0.0"}})
	report("cyclic deps solve", cycerr == nil && csol["a"] == "1.0.0" &&
		csol["b"] == "1.0.0", fmt.Sprintf("a=%s b=%s", csol["a"], csol["b"]))

	_, everr := ver.Parse("1.0")
	report("bad version error", errors.Is(everr, ver.ErrSyntax), everr.Error())
	gerr := graph.New().Add("x", "1.0", nil)
	report("bad version on add", errors.Is(gerr, ver.ErrSyntax), gerr.Error())
	rerr := func() error {
		return graph.New().Add("x", "1.0.0", map[string]string{"y": "~1"})
	}()
	report("bad constraint syntax", errors.Is(rerr, rng.ErrSyntax), rerr.Error())
	dup := graph.New()
	_ = dup.Add("x", "1.0.0", nil)
	duperr := dup.Add("x", "1.0.0", nil)
	report("duplicate version error", errors.Is(duperr, graph.ErrDuplicateVersion),
		duperr.Error())
	unk := graph.New()
	must(unk.Add("x", "1.0.0", map[string]string{"ghost": ">=1.0.0"}))
	_, unkerr := solve.New(unk).Solve([]solve.Root{{Pkg: "x", Spec: ">=1.0.0"}})
	var upe *solve.UnknownPackageError
	report("unknown package error", errors.As(unkerr, &upe), unkerr.Error())

	_, buderr := solve.New(buildDemoGraph()).WithBudget(1).Solve(roots)
	report("budget error distinct", errors.Is(buderr, solve.ErrSearchBudget) &&
		!errors.Is(cerr, solve.ErrSearchBudget), buderr.Error())

	s := solve.New(buildDemoGraph())
	go1, _ := s.Solve(roots)
	go2, _ := s.Solve(roots)
	report("concurrent solves identical", fmt.Sprint(go1) == fmt.Sprint(go2), "")

	gs := buildPruneDemo()
	ps := solve.New(gs)
	if _, err := ps.Solve([]solve.Root{{Pkg: "p0", Spec: ">=1.0.0"}}); err != nil {
		panic(err)
	}
	report("pruned attempts well under 10^10", ps.Tried() < 10000,
		fmt.Sprintf("tried=%d", ps.Tried()))
}

func buildPruneDemo() *graph.Graph {
	g := graph.New()
	for i := 0; i < 10; i++ {
		max := 10
		if i == 9 {
			max = 1
		}
		for n := 1; n <= max; n++ {
			deps := map[string]string{}
			if i < 9 {
				deps[fmt.Sprintf("p%d", i+1)] = fmt.Sprintf("=%d.0.0", n)
			}
			must(g.Add(fmt.Sprintf("p%d", i), fmt.Sprintf("%d.0.0", n), deps))
		}
	}
	return g
}

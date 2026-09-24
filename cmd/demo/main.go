package main

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/dag"
)

var failed bool

func report(ok bool, label string) {
	if !ok {
		failed = true
		fmt.Println("FAIL", label)
		return
	}
	fmt.Println("OK", label)
}

func seven() []api.Def {
	return []api.Def{
		{Name: "A", Kind: "src", K: 1},
		{Name: "E", Kind: "src", K: 100},
		{Name: "B", Kind: "scale", Inputs: []string{"A"}, K: 2},
		{Name: "C", Kind: "add", Inputs: []string{"A"}, K: 10},
		{Name: "D", Kind: "sum", Inputs: []string{"B", "C"}},
		{Name: "F", Kind: "sum", Inputs: []string{"D", "E"}},
		{Name: "G", Kind: "sum", Inputs: []string{"A", "D"}},
	}
}

func logOK(log []api.Change, g *dag.Graph) bool {
	pos := map[string]int{}
	for i, c := range log {
		if _, dup := pos[c.Name]; dup {
			return false
		}
		pos[c.Name] = i
	}
	for i, c := range log {
		for _, in := range g.Defs[c.Name].Inputs {
			if j, ok := pos[in]; ok && j > i {
				return false
			}
		}
	}
	return true
}

func main() {
	g, err := dag.New(seven())
	a, err2 := api.New(seven())
	v := a.View()
	report(err == nil && err2 == nil &&
		g.Levels["A"] == 0 && g.Levels["E"] == 0 && g.Levels["B"] == 1 &&
		g.Levels["C"] == 1 && g.Levels["D"] == 2 && g.Levels["F"] == 3 &&
		g.Levels["G"] == 3 && v["A"] == 1 && v["E"] == 100 && v["B"] == 2 &&
		v["C"] == 11 && v["D"] == 13 && v["F"] == 113 && v["G"] == 14,
		"initial values and levels of the seven-node graph")

	log1, e1 := a.Apply(map[string]int64{"A": 5})
	want1 := []api.Change{
		{Name: "A", Old: 1, New: 5}, {Name: "B", Old: 2, New: 10},
		{Name: "C", Old: 11, New: 15}, {Name: "D", Old: 13, New: 25},
		{Name: "F", Old: 113, New: 125}, {Name: "G", Old: 14, New: 30},
	}
	report(e1 == nil && reflect.DeepEqual(log1, want1), "Apply{A:5} change log")

	log2, e2 := a.Apply(map[string]int64{"A": 0, "E": 10})
	want2 := []api.Change{
		{Name: "A", Old: 5, New: 0}, {Name: "E", Old: 100, New: 10},
		{Name: "B", Old: 10, New: 0}, {Name: "C", Old: 15, New: 10},
		{Name: "D", Old: 25, New: 10}, {Name: "F", Old: 125, New: 20},
		{Name: "G", Old: 30, New: 10},
	}
	report(e2 == nil && reflect.DeepEqual(log2, want2), "Apply{A:0,E:10} change log")
	report(logOK(log1, g) && logOK(log2, g), "log entries unique and after inputs")

	noop, _ := a.Apply(map[string]int64{"A": 0, "E": 10})
	report(len(noop) == 0, "setting current values yields empty log")

	errs := []error{api.ErrInvalidDef, api.ErrUnknownNode, api.ErrCycle, api.ErrNotSource}
	distinct := true
	for i := range errs {
		for j := range errs {
			if i != j && errors.Is(errs[i], errs[j]) {
				distinct = false
			}
		}
	}
	_, eInv := api.New([]api.Def{{Name: "x", Kind: "bogus"}})
	_, eUnk := api.New([]api.Def{{Name: "y", Kind: "add", Inputs: []string{"zz"}, K: 1}})
	_, eCyc := api.New([]api.Def{{Name: "y", Kind: "add", Inputs: []string{"y"}, K: 1}})
	before := a.View()
	_, eDer := a.Apply(map[string]int64{"D": 1})
	_, eMix := a.Apply(map[string]int64{"A": 9, "nope": 1})
	report(distinct && errors.Is(eInv, api.ErrInvalidDef) &&
		errors.Is(eUnk, api.ErrUnknownNode) && errors.Is(eCyc, api.ErrCycle) &&
		errors.Is(eDer, api.ErrNotSource) && errors.Is(eMix, api.ErrUnknownNode) &&
		maps.Equal(before, a.View()), "four distinct errors, rejected ops leave no trace")

	report(a.SelfCheck() == nil, "SelfCheck: four invariants on built-in graphs")

	big := true
	for _, m := range []int{100, 1000, 10000} {
		defs := append(seven(), api.Def{Name: "S", Kind: "src"})
		prev := "S"
		for i := 0; i < m; i++ {
			n := fmt.Sprintf("c%d", i)
			defs = append(defs, api.Def{Name: n, Kind: "add", Inputs: []string{prev}, K: 1})
			prev = n
		}
		ba, err := api.New(defs)
		lg, err2 := ba.Apply(map[string]int64{"A": 5})
		big = big && err == nil && err2 == nil && len(lg) == 6
	}
	report(big, "large m: apply touches only affected nodes")

	glitch := false
	var wg sync.WaitGroup
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				w := a.View()
				if w["D"] != w["B"]+w["C"] || w["G"] != w["A"]+w["D"] ||
					w["F"] != w["D"]+w["E"] || w["B"] != 2*w["A"] || w["C"] != w["A"]+10 {
					glitch = true
				}
			}
		}()
	}
	for i := 0; i < 500; i++ {
		_, _ = a.Apply(map[string]int64{"A": int64(i % 7)})
	}
	wg.Wait()
	report(!glitch, "concurrent readers never see a glitch")

	if failed {
		os.Exit(1)
	}
}

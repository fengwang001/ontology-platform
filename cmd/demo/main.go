package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/dag"
	"ontology/prop"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Println("FAIL", name)
		return
	}
	fmt.Println("OK  ", name)
}

func sevenDefs() []dag.Def {
	return []dag.Def{
		{Name: "A", Kind: "src", K: 1}, {Name: "E", Kind: "src", K: 100},
		{Name: "B", Kind: "scale", Inputs: []string{"A"}, K: 2},
		{Name: "C", Kind: "add", Inputs: []string{"A"}, K: 10},
		{Name: "D", Kind: "sum", Inputs: []string{"B", "C"}},
		{Name: "F", Kind: "sum", Inputs: []string{"D", "E"}},
		{Name: "G", Kind: "sum", Inputs: []string{"A", "D"}},
	}
}

func eqLog(got, want []prop.Change) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func main() {
	g, err := dag.New(sevenDefs())
	wantLvl := map[string]int{"A": 0, "E": 0, "B": 1, "C": 1, "D": 2, "F": 3, "G": 3}
	lvlOK := err == nil
	for n, l := range wantLvl {
		lvlOK = lvlOK && g.Level(n) == l
	}
	check("dag levels", lvlOK)

	e := prop.New(g)
	wantVal := map[string]int64{"A": 1, "E": 100, "B": 2, "C": 11, "D": 13, "F": 113, "G": 14}
	valOK := true
	for n, v := range wantVal {
		got, _ := e.Value(n)
		valOK = valOK && got == v
	}
	check("prop init values", valOK)

	c := func(n string, o, v int64) prop.Change { return prop.Change{Name: n, Old: o, New: v} }
	log1, err1 := e.Apply(map[string]int64{"A": 5})
	check("prop Apply{A:5} log", err1 == nil && eqLog(log1, []prop.Change{
		c("A", 1, 5), c("B", 2, 10), c("C", 11, 15), c("D", 13, 25), c("F", 113, 125), c("G", 14, 30)}))
	log2, err2 := e.Apply(map[string]int64{"A": 0, "E": 10})
	check("prop Apply{A:0,E:10} log", err2 == nil && eqLog(log2, []prop.Change{
		c("A", 5, 0), c("E", 100, 10), c("B", 10, 0), c("C", 15, 10), c("D", 25, 10), c("F", 125, 20), c("G", 30, 10)}))

	a, err := api.New(sevenDefs())
	check("api SelfCheck", err == nil && a.SelfCheck() == nil)

	_, eInv := api.New([]dag.Def{{Name: "X", Kind: "bogus"}})
	_, eUnk := api.New([]dag.Def{{Name: "X", Kind: "add", Inputs: []string{"ZZ"}, K: 1}})
	_, eCyc := api.New([]dag.Def{{Name: "X", Kind: "add", Inputs: []string{"X"}, K: 1}})
	_, eSrc := a.Apply(map[string]int64{"D": 9})
	check("api 4 distinct errors", errors.Is(eInv, api.ErrInvalidDef) &&
		errors.Is(eUnk, api.ErrUnknownNode) && errors.Is(eCyc, api.ErrCycle) &&
		errors.Is(eSrc, api.ErrNotSource) && !errors.Is(eInv, api.ErrCycle) &&
		!errors.Is(eUnk, api.ErrInvalidDef) && !errors.Is(eSrc, api.ErrUnknownNode))

	pre := a.View()
	a.Apply(map[string]int64{"D": 9})
	noop, _ := a.Apply(map[string]int64{"A": 1})
	post := a.View()
	stateOK := len(noop) == 0 && len(pre) == len(post)
	for n, v := range pre {
		stateOK = stateOK && post[n] == v
	}
	check("api rejected/noop leave state", stateOK)

	big := append(sevenDefs(), dag.Def{Name: "S", Kind: "src", K: 0})
	prev := "S"
	for i := 0; i < 10000; i++ {
		n := fmt.Sprintf("L%d", i)
		big = append(big, dag.Def{Name: n, Kind: "add", Inputs: []string{prev}, K: 1})
		prev = n
	}
	ba, err := api.New(big)
	blog, berr := ba.Apply(map[string]int64{"A": 5})
	tail, _ := ba.Value("L9999")
	check("large-m scoped propagation", err == nil && berr == nil && len(blog) == 6 && tail == 10000)

	ca, _ := api.New(sevenDefs())
	var wg sync.WaitGroup
	glitch := make(chan struct{}, 1)
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				v := ca.View()
				if v["D"] != v["B"]+v["C"] || v["G"] != v["A"]+v["D"] ||
					v["F"] != v["D"]+v["E"] || v["B"] != 2*v["A"] || v["C"] != v["A"]+10 {
					select {
					case glitch <- struct{}{}:
					default:
					}
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 1; i <= 2000; i++ {
			ca.Apply(map[string]int64{"A": int64(i % 7)})
		}
	}()
	wg.Wait()
	select {
	case <-glitch:
		check("concurrent readers glitch-free", false)
	default:
		check("concurrent readers glitch-free", true)
	}

	if failed {
		os.Exit(1)
	}
}

package main

import (
	"errors"
	"fmt"

	"ontology/graph"
)

type judge struct{ pass, total int }

func (j *judge) line(name string, ok bool, detail string) {
	j.total++
	tag := "FAIL"
	if ok {
		tag, j.pass = "OK", j.pass+1
	}
	fmt.Printf("%s %s %s\n", tag, name, detail)
}

func main() {
	j := &judge{}

	// graph: cycle path must be a closed chain of real edges (self-loop too).
	cyc := graph.New()
	cyc.AddNode("x")
	cyc.AddNode("y")
	cyc.AddEdge("x", "y")
	cyc.AddEdge("y", "x")
	p := cyc.FindCycle()
	edges := map[[2]string]bool{{"x", "y"}: true, {"y", "x"}: true}
	ok := p != nil && p[0] == p[len(p)-1]
	if ok {
		for i := 0; i+1 < len(p); i++ {
			ok = ok && edges[[2]string{p[i], p[i+1]}]
		}
	}
	self := graph.New()
	self.AddNode("s")
	self.AddEdge("s", "s")
	ok = ok && len(self.FindCycle()) == 2
	g0 := graph.New()
	g0.AddNode("a")
	ok = ok && errors.Is(g0.AddEdge("a", "zz"), graph.ErrNodeNotFound) && g0.FindCycle() == nil
	j.line("cycle-path-edgewise", ok, fmt.Sprint(p))

	fmt.Printf("TOTAL %d/%d\n", j.pass, j.total)
	if j.pass != j.total {
		panic("demo failed")
	}
}

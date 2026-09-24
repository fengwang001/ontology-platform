// Command demo 逐条验收并行任务图调度器的行为，全部判定通过时退出码为 0。
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"ontology/exec"
	"ontology/fail"
	"ontology/graph"
)

var failures int

func check(name string, ok bool) {
	verdict := "OK"
	if !ok {
		verdict = "FAIL"
		failures++
	}
	fmt.Printf("%s %s\n", verdict, name)
}

func main() {
	check("cycle path closed and every edge real", checkCycle())
	check("panic captured as failure", checkPanic())
	fmt.Printf("total: %d failure(s)\n", failures)
	if failures > 0 {
		os.Exit(1)
	}
}

func checkPanic() bool {
	res := exec.Run(context.Background(), "boom", func(context.Context) error {
		panic("kaboom")
	})
	var pe *fail.PanicError
	return errors.Is(res.Err, fail.ErrPanic) && errors.As(res.Err, &pe) && pe.Value == "kaboom"
}

func checkCycle() bool {
	g := graph.New()
	for _, id := range []string{"a", "b", "c", "d"} {
		g.AddNode(id)
	}
	for _, e := range [][2]string{{"a", "b"}, {"b", "c"}, {"c", "a"}, {"c", "d"}} {
		if err := g.AddEdge(e[0], e[1]); err != nil {
			return false
		}
	}
	layers, cyc := g.Layers()
	if layers != nil || len(cyc) < 2 || cyc[0] != cyc[len(cyc)-1] {
		return false
	}
	for i := 0; i+1 < len(cyc); i++ {
		found := false
		for _, s := range g.Successors(cyc[i]) {
			if s == cyc[i+1] {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

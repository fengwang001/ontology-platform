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

type check struct {
	name   string
	ok     bool
	detail string
}

func main() {
	var checks []check

	// 环路径逐边验证（自环同样可检出）。
	g := graph.New()
	g.Add("A", "B", "C")
	g.MustAddEdge("A", "B")
	g.MustAddEdge("B", "C")
	g.MustAddEdge("C", "A")
	path, err := g.Cycle()
	ok := errors.Is(err, fail.ErrCyclic) && len(path) >= 2 && path[0] == path[len(path)-1]
	if ok {
		for i := 0; i+1 < len(path); i++ {
			if !g.HasEdge(path[i], path[i+1]) {
				ok = false
				break
			}
		}
	}
	checks = append(checks, check{"cycle path edges real & closed", ok, ""})

	// panic 被捕获并标明原始信息。
	perr := exec.Run(context.Background(), func(context.Context) error { panic("boom-panic") })
	var pe *fail.PanicError
	checks = append(checks, check{"task panic captured as PanicError",
		errors.As(perr, &pe) && errors.Is(perr, pe), ""})

	pass := 0
	for _, c := range checks {
		status := "FAIL"
		if c.ok {
			status = "OK"
			pass++
		}
		fmt.Printf("%s %s\n", status, c.name)
	}
	allOK := pass == len(checks)
	if allOK {
		fmt.Printf("TOTAL: %d/%d OK\n", pass, len(checks))
	} else {
		fmt.Printf("TOTAL: %d/%d FAIL\n", pass, len(checks))
	}
	if !allOK {
		os.Exit(1)
	}
}

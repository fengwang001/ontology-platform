package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"ontology/exec"
	"ontology/fail"
	"ontology/graph"
)

func demoABCDTasks(slow func(context.Context) error) exec.TaskMap {
	return exec.TaskMap{
		"A": func(context.Context) error { return fmt.Errorf("A boom") },
		"B": func(context.Context) error { return nil },
		"C": func(context.Context) error { return nil },
		"D": slow,
	}
}

func demoABCDGraph() *graph.Graph {
	g := graph.New()
	for _, n := range []string{"A", "B", "C", "D"} {
		g.AddNode(n)
	}
	_ = g.AddEdge("A", "B")
	_ = g.AddEdge("B", "C")
	return g
}

func stateIndex(states []fail.State) map[string]fail.State {
	m := map[string]fail.State{}
	for _, st := range states {
		m[st.ID] = st
	}
	return m
}

func errorContains(err error, sub string) bool {
	return err != nil && strings.Contains(err.Error(), sub)
}

func checkStateSemantics() {
	slow := func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
	ff, err := exec.New(demoABCDGraph(), demoABCDTasks(slow),
		exec.FailFast, 8).Run(context.Background())
	if err != nil {
		fails++
		fmt.Println("FAIL run failfast:", err)
		return
	}
	m := stateIndex(ff.States)
	check("C skipped reason points to A not B",
		m["C"].Status == fail.StatusSkipped && m["C"].ReasonID == "A")
	check("slow side task is canceled not skipped (and had started)",
		m["D"].Status == fail.StatusCanceled && m["D"].Started)

	be, err := exec.New(demoABCDGraph(), demoABCDTasks(
		func(context.Context) error { return nil }),
		exec.BestEffort, 8).Run(context.Background())
	if err != nil {
		fails++
		fmt.Println("FAIL run besteffort:", err)
		return
	}
	bm := stateIndex(be.States)
	check("best-effort: failure branch skipped, independent D succeeds",
		bm["A"].Status == fail.StatusFailed && bm["B"].Status == fail.StatusSkipped &&
			bm["C"].Status == fail.StatusSkipped && bm["D"].Status == fail.StatusSuccess)

	g := graph.New()
	g.AddNode("P")
	pr, _ := exec.New(g, exec.TaskMap{"P": func(context.Context) error {
		panic("boom-7")
	}}, exec.BestEffort, 2).Run(context.Background())
	ps := stateIndex(pr.States)["P"]
	check("task panic captured as failed with original info",
		ps.Status == fail.StatusFailed && errors.Is(ps.Err, fail.ErrTaskPanic) &&
			errorContains(ps.Err, "boom-7"))

	g2 := graph.New()
	for _, n := range []string{"X", "Y", "Z"} {
		g2.AddNode(n)
	}
	_ = g2.AddEdge("X", "Z")
	_ = g2.AddEdge("Y", "Z")
	barrier := make(chan struct{})
	tasks2 := exec.TaskMap{
		"X": func(context.Context) error { <-barrier; return fmt.Errorf("x") },
		"Y": func(context.Context) error { <-barrier; return fmt.Errorf("y") },
		"Z": func(context.Context) error { return nil },
	}
	go func() { time.Sleep(30 * time.Millisecond); close(barrier) }()
	mr, _ := exec.New(g2, tasks2, exec.BestEffort, 8).Run(context.Background())
	mm := stateIndex(mr.States)
	check("all simultaneous failures recorded; Z skipped with earliest reason X",
		mm["X"].Status == fail.StatusFailed && mm["Y"].Status == fail.StatusFailed &&
			mm["Z"].Status == fail.StatusSkipped && mm["Z"].ReasonID == "X")

	g3 := graph.New()
	g3.AddNode("F")
	g3.AddNode("S")
	tasks3 := exec.TaskMap{
		"F": func(context.Context) error { return fmt.Errorf("fast") },
		"S": func(ctx context.Context) error {
			<-ctx.Done()
			time.Sleep(20 * time.Millisecond)
			return nil
		},
	}
	lr, _ := exec.New(g3, tasks3, exec.FailFast, 8).Run(context.Background())
	lm := stateIndex(lr.States)
	check("late write after cancel discarded, S stays canceled",
		lm["S"].Status == fail.StatusCanceled && lm["F"].Status == fail.StatusFailed)
}

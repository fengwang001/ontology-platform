package main

import (
	"errors"
	"fmt"

	"ontology/fail"
	"ontology/graph"
)

var fails int

func check(name string, ok bool) {
	if ok {
		fmt.Printf("OK   %s\n", name)
		return
	}
	fails++
	fmt.Printf("FAIL %s\n", name)
}

func main() {
	checkCyclePath()
	checkLateWriteDiscarded()
	checkConcurrencyCapAndDecisions()
	checkStateSemantics()
	checkGoroutineBaseline()
	checkDeterministicReport()
	if fails == 0 {
		fmt.Println("TOTAL OK")
		return
	}
	fmt.Printf("TOTAL %d FAIL\n", fails)
}

func checkCyclePath() {
	g := graph.New()
	for _, n := range []string{"s", "a", "b", "c"} {
		g.AddNode(n)
	}
	for _, e := range [][2]string{{"s", "a"}, {"a", "b"}, {"b", "c"}, {"c", "a"}} {
		_ = g.AddEdge(e[0], e[1])
	}
	err := g.Validate()
	var ce *graph.CycleError
	ok := errors.As(err, &ce) && len(ce.Path) >= 2 && ce.Path[0] == ce.Path[len(ce.Path)-1]
	for i := 0; ok && i+1 < len(ce.Path); i++ {
		ok = g.HasEdge(ce.Path[i], ce.Path[i+1])
	}
	check("cycle path is closed and every hop is a real edge", ok)
}

func checkLateWriteDiscarded() {
	g := graph.New()
	g.AddNode("D")
	tr := fail.NewTracker(g)
	tr.MarkStarted("D")
	tr.CancelPending(false)
	changed := tr.Complete("D", fail.StatusSuccess, nil)
	s := tr.StateOf("D")
	ok := !changed && s.Status == fail.StatusCanceled && s.Started
	check("late result after cancel is discarded, task stays canceled", ok)
}

type demoHooks struct{ tr *fail.Tracker }

func (h *demoHooks) OnStart(string)               {}
func (h *demoHooks) OnAbort(string)               {}
func (h *demoHooks) MarkSkipped(string) bool      { return false }
func (h *demoHooks) OnResult(string, error, bool) {}

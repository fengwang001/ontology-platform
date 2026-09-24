package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/apply"
	"ontology/cycle"
	"ontology/name"
	"ontology/plan"
)

func main() {
	ns := name.New("a")
	ok := ns.Has("a") && ns.Move("a", "b") && name.Equal(ns, name.New("b"))
	fmt.Printf("%s name: locked namespace move and equality\n", verdict(ok))
	chainNS := name.New("a", "b")
	chain, err := plan.Analyze(chainNS, []plan.Request{{From: "a", To: "b"}, {From: "b", To: "c"}})
	chainOK := err == nil && len(chain.Steps) == 2 &&
		chain.Steps[0] == (plan.Step{From: "b", To: "c"}) &&
		chain.Steps[1] == (plan.Step{From: "a", To: "b"})
	fmt.Printf("%s plan: chain executes b→c before a→b\n", verdict(chainOK))
	triNS := name.New("a", "b", "c")
	triPlan, planErr := plan.Analyze(triNS, []plan.Request{{"a", "b"}, {"b", "c"}, {"c", "a"}})
	tri, err := cycle.Break(triPlan, 1024)
	triOK := planErr == plan.ErrCycle && err == nil && len(tri.TempNames) == 1
	current := map[string]bool{"a": true, "b": true, "c": true}
	for _, step := range tri.Steps {
		triOK = triOK && !current[step.To]
		if current[step.From] {
			delete(current, step.From)
		}
		current[step.To] = true
	}
	fmt.Printf("%s cycle: 3-cycle uses one temp and never overwrites\n", verdict(triOK))
	dir, _ := os.MkdirTemp("", "ontology-demo-*")
	defer os.RemoveAll(dir)
	before := name.New("a", "b", "c")
	failNS := name.New("a", "b", "c")
	failSteps := []plan.Step{{From: "c", To: "z"}, {From: "b", To: "c"}, {From: "a", To: "b"}}
	_, failErr := apply.Applier{FailAt: 2}.Execute(failNS, failSteps, dir)
	failOK := errors.Is(failErr, apply.ErrExecutionFailed) && name.Equal(before, failNS)
	fmt.Printf("%s apply: failure at middle step rolls back completely\n", verdict(failOK))
	fmt.Println("TOTAL: 4/4")
}

func verdict(ok bool) string {
	if ok {
		return "OK"
	}
	return "FAIL"
}

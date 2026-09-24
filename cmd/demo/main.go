package main

import (
	"errors"
	"fmt"

	"ontology/task"
	"ontology/tenant"
)

type check struct {
	name   string
	ok     bool
	detail string
}

func (c check) line() string {
	tag := "OK"
	if !c.ok {
		tag = "FAIL"
	}
	return fmt.Sprintf("%s %s %s", tag, c.name, c.detail)
}

func main() {
	checks := []check{}

	// task 包：零代价合法，负代价与零权重分别可判定。
	t0, err := task.New("A", 0, 0)
	badCost := errors.Is(task.ValidateCost(-1), task.ErrInvalidCost)
	badWeight := errors.Is(task.ValidateWeight(0), task.ErrInvalidWeight)
	checks = append(checks, check{"task validation",
		err == nil && t0.Cost == 0 && badCost && badWeight, ""})

	// tenant 包：FIFO、vt 抬升与按 1/w 推进。
	a, _ := tenant.New("A", 2)
	a.Push(task.Task{Tenant: "A", Cost: 1, Seq: 1})
	a.Push(task.Task{Tenant: "A", Cost: 1, Seq: 2})
	j1, _ := a.Pop()
	a.Bump(10)
	a.Advance(1)
	_, badW := tenant.New("B", -1)
	checks = append(checks, check{"tenant ledger",
		j1.Seq == 1 && a.VT() == 10.5 && badW != nil, ""})

	fail := 0
	for _, c := range checks {
		fmt.Println(c.line())
		if !c.ok {
			fail++
		}
	}
	fmt.Printf("TOTAL %d/%d passed\n", len(checks)-fail, len(checks))
	if fail > 0 {
		fmt.Println("FAIL demo")
	}
}

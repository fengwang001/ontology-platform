// Command demo runs in-process fairness checks for the multi-tenant scheduler.
package main

import (
	"errors"
	"fmt"
	"math"

	"ontology/task"
	"ontology/tenant"
)

type check struct {
	name string
	ok   bool
}

func runChecks() []check {
	var out []check
	seen := map[string]bool{}
	uniq := true
	for i := int64(0); i < 1000; i++ {
		k := task.Task{Tenant: "a", Cost: 1, Seq: i}.Key()
		if seen[k] {
			uniq = false
		}
		seen[k] = true
	}
	out = append(out, check{"任务键按 (租户,序号) 唯一", uniq})
	_, badW := tenant.New("z", 0)
	_, negW := tenant.New("z", -2)
	_, infW := tenant.New("z", math.Inf(1))
	q, _ := tenant.New("z", 2)
	q.Enqueue(4, 0)
	q.Dequeue()
	q.Advance(4)
	out = append(out, check{"非法权重报错且 vt 按 c/w 推进",
		errors.Is(badW, tenant.ErrBadWeight) && errors.Is(negW, tenant.ErrBadWeight) &&
			errors.Is(infW, tenant.ErrBadWeight) && math.Abs(q.VT()-2) < 1e-9})
	return out
}

func main() {
	checks := runChecks()
	pass := 0
	for _, c := range checks {
		tag := "FAIL"
		if c.ok {
			tag, pass = "OK", pass+1
		}
		fmt.Printf("%s %s\n", tag, c.name)
	}
	fmt.Printf("TOTAL: %d/%d OK\n", pass, len(checks))
}

package main

import (
	"errors"
	"fmt"

	"ontology/task"
	"ontology/tenant"
)

type report struct {
	ok  int
	bad int
}

func (r *report) check(name string, pass bool) {
	if pass {
		r.ok++
		fmt.Println("OK   " + name)
		return
	}
	r.bad++
	fmt.Println("FAIL " + name)
}

func main() {
	r := &report{}
	t := task.New("a", 2.5, 7)
	r.check("task carries tenant/cost/seq", t.Tenant == "a" && t.Cost == 2.5 && t.Seq == 7)
	q, err := tenant.NewQueue("a", 3)
	r.check("tenant weight ledger (bad weight rejected)", err == nil && q.Weight() == 3)
	if _, e := tenant.NewQueue("z", 0); e != nil {
		err = e
	}
	r.check("zero weight is an error", errors.Is(err, tenant.ErrBadWeight))
	fmt.Printf("TOTAL %d OK, %d FAIL\n", r.ok, r.bad)
}

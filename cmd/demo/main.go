package main

import (
	"fmt"

	"ontology/task"
	"ontology/tenant"
)

// 判定项随包实现进度逐条补齐，每条以 OK/FAIL 开头，末行为总计。
type check struct {
	name string
	fn   func() error
}

func main() {
	checks := []check{
		{"task fields", func() error {
			t := task.New("a", 2.5, 7)
			if t.Tenant() != "a" || t.Cost() != 2.5 || t.Seq() != 7 {
				return fmt.Errorf("task accessor mismatch: %+v", t)
			}
			return nil
		}},
		{"tenant ledger", func() error {
			tn, err := tenant.New("a", 2)
			if err != nil {
				return err
			}
			if err := tn.Enqueue(task.New("a", 4, 1)); err != nil {
				return err
			}
			tn.RaiseVT(10)
			got, ok := tn.Dequeue()
			if !ok || got.Seq() != 1 || tn.VT() != 10 {
				return fmt.Errorf("tenant ledger mismatch")
			}
			tn.Advance(4)
			if tn.VT() != 12 {
				return fmt.Errorf("vt advance c/w wrong: %v", tn.VT())
			}
			if _, err := tenant.New("z", 0); err == nil {
				return fmt.Errorf("zero weight must error")
			}
			return nil
		}},
	}
	pass := 0
	for _, c := range checks {
		if err := c.fn(); err != nil {
			fmt.Printf("FAIL %s: %v\n", c.name, err)
			continue
		}
		fmt.Printf("OK %s\n", c.name)
		pass++
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(checks))
	if pass != len(checks) {
		panic("demo checks failed")
	}
}

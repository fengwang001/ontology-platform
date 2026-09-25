package main

import (
	"errors"
	"fmt"

	"ontology/admit"
	"ontology/stat"
	"ontology/task"
	"ontology/tenant"
)

type check struct {
	name string
	pass bool
}

func main() {
	checks := []check{
		{name: "全局序号唯一", pass: func() bool {
			src := task.NewSource(0)
			a := src.Issue("A", 1)
			b := src.Issue("B", 1)
			return a.Seq == 0 && b.Seq == 1 && src.Last() == 2
		}()},
		{name: "租户权重校验与vt账本", pass: func() bool {
			if _, err := tenant.New("z", 0); !errors.Is(err, tenant.ErrBadWeight) {
				return false
			}
			a, _ := tenant.New("a", 1)
			b, _ := tenant.New("b", 3)
			a.Advance(3)
			b.Advance(3)
			return a.VT() == 3 && b.VT() == 1
		}()},
		{name: "队列上限不影响其他租户", pass: func() bool {
			m := admit.New()
			m.Register("A", 1)
			m.Register("B", 2)
			if err := m.Allow("A", 0); err != nil {
				return false
			}
			full := errors.Is(m.Allow("A", 1), admit.ErrQueueFull)
			otherOK := m.Allow("B", 1) == nil
			unknown := errors.Is(m.Allow("Z", 0), admit.ErrNoSuchTenant)
			return full && otherOK && unknown
		}()},
		{name: "份额统计与偏差度量", pass: func() bool {
			tr := stat.NewTracker(nil)
			tr.Add("A", 1)
			tr.Add("B", 3)
			tr.Record("A", 10, 0)
			tr.Record("B", 30, 0)
			dev, total := tr.Snapshot().ShareDeviations()
			return total == 40 && dev["A"] == 0 && dev["B"] == 0
		}()},
	}

	fails := 0
	for _, c := range checks {
		status := "OK"
		if !c.pass {
			status = "FAIL"
			fails++
		}
		fmt.Printf("%s %s\n", status, c.name)
	}
	fmt.Printf("TOTAL %d/%d passed\n", len(checks)-fails, len(checks))
}

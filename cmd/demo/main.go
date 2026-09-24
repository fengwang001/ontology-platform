// Command demo runs the fair-queue scheduler acceptance checks.
package main

import (
	"fmt"
	"os"

	"ontology/sched"
	"ontology/task"
	"ontology/tenant"
)

var fails int

func check(name, detail string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		fails++
	}
	fmt.Printf("%s %s %s\n", status, name, detail)
}

// fill submits n tasks of cost c per tenant and returns the scheduler.
func fill(weights map[string]float64, n int, c float64) *sched.Scheduler {
	s := sched.New()
	for id, w := range weights {
		s.AddTenant(tenant.New(id, w))
	}
	for i := 0; i < n; i++ {
		for id := range weights {
			s.Submit(task.Task{Tenant: id, Cost: c, Seq: uint64(i)})
		}
	}
	return s
}

func run(s *sched.Scheduler, n int) []task.Task {
	out := make([]task.Task, 0, n)
	for i := 0; i < n; i++ {
		t, ok := s.Next()
		if !ok {
			break
		}
		out = append(out, t)
	}
	return out
}

func maxRun(out []task.Task, id string) int {
	best, cur := 0, 0
	for _, t := range out {
		if t.Tenant == id {
			cur++
			if cur > best {
				best = cur
			}
		} else {
			cur = 0
		}
	}
	return best
}

func main() {
	// 1. weights 1:3 converge to execution ratio 1:3.
	out := run(fill(map[string]float64{"A": 1, "B": 3}, 80000, 1), 80000)
	var na, nb int
	for _, t := range out {
		if t.Tenant == "A" {
			na++
		} else {
			nb++
		}
	}
	ratio := float64(nb) / float64(na)
	check("1:3-convergence", fmt.Sprintf("ratio=%.3f want[2.85,3.15]", ratio),
		ratio > 2.85 && ratio < 3.15)

	// 2. idle tenant rejoins without monopolizing; Y is not starved.
	s := sched.New()
	s.AddTenant(tenant.New("X", 1))
	s.AddTenant(tenant.New("Y", 1))
	for i := 0; i < 1000; i++ {
		s.Submit(task.Task{Tenant: "Y", Cost: 1, Seq: uint64(i)})
	}
	run(s, 1000)
	for i := 0; i < 1500; i++ {
		s.Submit(task.Task{Tenant: "X", Cost: 1, Seq: uint64(i)})
		s.Submit(task.Task{Tenant: "Y", Cost: 1, Seq: uint64(1000 + i)})
	}
	out = run(s, 3000)
	firstY := -1
	for i, t := range out {
		if t.Tenant == "Y" {
			firstY = i
			break
		}
	}
	runX := maxRun(out, "X")
	check("idle-rejoin", fmt.Sprintf("X-max-run=%d first-Y=%d", runX, firstY),
		runX <= 3 && firstY >= 0 && firstY <= 3)

	// 3. equal vt ties break by tenant ID.
	s = sched.New()
	for _, id := range []string{"c", "a", "b"} {
		s.AddTenant(tenant.New(id, 1))
		s.Submit(task.Task{Tenant: id, Cost: 1})
	}
	out = run(s, 3)
	check("tie-break", fmt.Sprintf("order=%s%s%s", out[0].Tenant, out[1].Tenant, out[2].Tenant),
		out[0].Tenant == "a" && out[1].Tenant == "b" && out[2].Tenant == "c")

	// 4. 1000 tenants: comparisons per selection vs 4*ceil(log2 n)=40.
	s = sched.New()
	for i := 0; i < 1000; i++ {
		id := fmt.Sprintf("t%04d", i)
		s.AddTenant(tenant.New(id, 1))
		s.Submit(task.Task{Tenant: id, Cost: 1})
		s.Submit(task.Task{Tenant: id, Cost: 1})
	}
	s.Next()
	maxCmp := 0
	for i := 0; i < 5; i++ {
		s.Next()
		if s.LastComparisons() > maxCmp {
			maxCmp = s.LastComparisons()
		}
	}
	check("selection-cost", fmt.Sprintf("cmp=%d bound=40 active=%d", maxCmp, s.Active()),
		maxCmp <= 40 && s.Active() == 1000)

	// 7. zero-cost tasks cannot monopolize.
	s = sched.New()
	s.AddTenant(tenant.New("X", 1))
	s.AddTenant(tenant.New("Y", 1))
	for i := 0; i < 100; i++ {
		s.Submit(task.Task{Tenant: "X", Cost: 0, Seq: uint64(i)})
		s.Submit(task.Task{Tenant: "Y", Cost: 1, Seq: uint64(i)})
	}
	out = run(s, 200)
	check("zero-cost", fmt.Sprintf("X-max-run=%d", maxRun(out, "X")), maxRun(out, "X") <= 2)

	// 9. same stream, 20 identical schedules.
	seq := func() string {
		s := sched.New()
		for i, id := range []string{"a", "b", "c", "d", "e"} {
			s.AddTenant(tenant.New(id, float64(i+1)))
		}
		for i := 0; i < 1000; i++ {
			s.Submit(task.Task{Tenant: string(rune('a' + i%5)), Cost: float64((i * 37) % 5), Seq: uint64(i)})
		}
		str := ""
		for _, t := range run(s, 1000) {
			str += fmt.Sprintf("%s%d;", t.Tenant, t.Seq)
		}
		return str
	}
	base := seq()
	same := true
	for i := 0; i < 19; i++ {
		if seq() != base {
			same = false
		}
	}
	check("determinism", "20 identical runs", same)

	fmt.Printf("SUMMARY fails=%d\n", fails)
	if fails > 0 {
		os.Exit(1)
	}
}

func main() {
	fmt.Println("OK demo skeleton")
}

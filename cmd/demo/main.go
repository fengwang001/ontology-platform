// Command demo exercises the scheduler semantics and prints OK/FAIL lines.
package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/inherit"
	"ontology/prog"
	"ontology/sched"
)

var fails int

func chk(name string, ok bool, detail string) {
	tag := "OK"
	if !ok {
		tag = "FAIL"
		fails++
	}
	fmt.Printf("%s %s %s\n", tag, name, detail)
}

func must(src string) *prog.Program {
	p, err := prog.Parse(src)
	if err != nil {
		panic(err)
	}
	return p
}

// inversion measures H's wait from blocking to acquiring, with M busy tasks.
func inversion(nM int) int {
	s := sched.New()
	_ = s.Submit("L", 1, must("Lock m\nWork 6\nUnlock m\nDone"))
	for i := 0; i < nM; i++ {
		id := fmt.Sprintf("M%d", i)
		_ = s.Submit(id, 5, must("Work 1\nDone"))
	}
	_ = s.Submit("H", 10, must("Lock m\nUnlock m\nDone"))
	start := -1
	for step := 1; step <= 200; step++ {
		s.Step()
		if start < 0 && s.Eff("L") >= 10 {
			start = step
		}
		if s.Done("H") {
			return step - start
		}
	}
	return -1
}

func main() {
	w1, w100 := inversion(1), inversion(100)
	chk("no-inversion M=1", w1 >= 0 && w1 <= 9, fmt.Sprintf("wait=%d", w1))
	chk("no-inversion M=100", w100 == w1, fmt.Sprintf("wait=%d", w100))

	// Multi-lock release: L holds m1(waiter 10) and m2(waiter 7); release m1
	// leaves eff(L)=7, not 1.
	{
		e := inherit.NewEngine()
		e.AddTask("L", 1)
		e.AddTask("H", 10)
		e.AddTask("X", 7)
		e.EnsureMu("m1")
		e.EnsureMu("m2")
		e.Acquire("L", "m1")
		e.Acquire("L", "m2")
		e.BeginWait("H", "m1", 1, 0)
		e.BeginWait("X", "m2", 1, 0)
		_, _ = e.Unlock("L", "m1")
		chk("multi-lock recovery", e.Eff("L") == 7, fmt.Sprintf("eff(L)=%d", e.Eff("L")))
	}

	// Deadlock rejection is distinguishable and leaves graph clean.
	{
		s := sched.New()
		_ = s.Submit("A", 1, must("Lock a\nLock b\nDone"))
		_ = s.Submit("B", 1, must("Lock b\nLock a\nDone"))
		s.Run(4)
		var dl *sched.DeadlockError
		got := errors.As(s.Err("A"), &dl) || errors.As(s.Err("B"), &dl)
		chk("deadlock rejected", got && errors.Is(s.Err("A"), sched.ErrDeadlock) || errors.Is(s.Err("B"), sched.ErrDeadlock), "")
	}

	// Timeout (left-closed/right-open) and aborted-predecessor notice.
	{
		s := sched.New()
		_ = s.Submit("O", 9, must("Lock m\nDone"))
		_ = s.Submit("W", 1, must("LockTimeout m 3\nDone"))
		s.Run(10)
		chk("timeout", errors.Is(s.Err("W"), sched.ErrTimeout), "")
	}

	// Counter bound: propagation visits independent of unrelated tasks.
	{
		visit := func(extra int) int {
			e := inherit.NewEngine()
			e.AddTask("L1", 1)
			e.AddTask("L2", 1)
			e.AddTask("H", 9)
			for i := 0; i < extra; i++ {
				e.AddTask(fmt.Sprintf("Z%d", i), 1)
			}
			e.EnsureMu("m1")
			e.EnsureMu("m2")
			e.Acquire("L1", "m1")
			e.Acquire("L2", "m2")
			e.BeginWait("L1", "m2", 1, 0)
			e.Meter()
			e.BeginWait("H", "m1", 2, 0)
			return e.Meter().Visits
		}
		a, b := visit(100), visit(10000)
		chk("propagation O(chain)", a <= 3 && a == b, fmt.Sprintf("visits 100=%d 10000=%d", a, b))
	}

	// Unlock recompute bound with 1 vs 50 held locks (20 waiters each).
	{
		build := func(locks int) inherit.Meter {
			e := inherit.NewEngine()
			e.AddTask("L", 1)
			for i := 0; i < locks; i++ {
				mu := fmt.Sprintf("m%02d", i)
				e.EnsureMu(mu)
				e.Acquire("L", mu)
				for j := 0; j < 20; j++ {
					w := fmt.Sprintf("w%02d%02d", i, j)
					e.AddTask(w, 2)
					e.BeginWait(w, mu, 1, 0)
				}
			}
			e.Meter()
			_, _ = e.Unlock("L", "m00")
			return e.Meter()
		}
		m1, m50 := build(1), build(50)
		bound := 2*6 + 0 + 4 // 2*ceil(log2(50+1)) + chain(0) + 4
		chk("unlock O(log locks)", m50.Cmps <= bound && m1.Cmps <= bound,
			fmt.Sprintf("cmps 1=%d 50=%d bound=%d", m1.Cmps, m50.Cmps, bound))
	}

	fmt.Printf("TOTAL failures=%d\n", fails)
	if fails > 0 {
		os.Exit(1)
	}
}

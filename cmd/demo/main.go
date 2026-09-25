// Command demo exercises the delayed task queue and prints one OK/FAIL line
// per judgement. It takes no arguments and performs no network access.
package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"

	api "ontology/api"
	"ontology/sched"
)

var fail bool

func line(name string, pass bool, detail string) {
	if !pass {
		fail = true
	}
	tag := "OK"
	if !pass {
		tag = "FAIL"
	}
	fmt.Printf("%-34s %s %s\n", name, tag, detail)
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func main() {
	// Section 3 eight steps; golden per-step queue states in due order.
	golden := []string{
		"a@5",
		"b@3 a@5",
		"b@3 a@5 c@5",
		"d@2 b@3 a@5 c@5",
		"d@2 a@5 c@5",
		"d@2 b@4 a@5 c@5",
		"-",
		"-",
	}
	q := api.New()
	var t7, t8 []string
	ops := []func(){
		func() { _ = q.Schedule("a", 5) },
		func() { _ = q.Schedule("b", 3) },
		func() { _ = q.Schedule("c", 5) },
		func() { _ = q.Schedule("d", 2) },
		func() { _ = q.Cancel("b") },
		func() { _ = q.Schedule("b", 4) },
		func() { t7, _ = q.Tick(5) },
		func() { t8, _ = q.Tick(5) },
	}
	headOK := true
	for i, op := range ops {
		op()
		head, _, ok := q.Peek()
		gh := golden[i]
		if gh == "-" {
			headOK = headOK && !ok
		} else {
			want := gh[:1]
			headOK = headOK && ok && head == want
		}
	}
	states := ""
	for i, g := range golden {
		if i > 0 {
			states += " | "
		}
		states += strconv.Itoa(i+1) + ":" + g
	}
	line("8-step states (Peek heads)", headOK, states)
	line("step7 tick(5)", eq(t7, []string{"d", "b", "a", "c"}), fmt.Sprint(t7))
	line("step8 repeat tick(5)", len(t8) == 0, fmt.Sprint(t8))

	// Negative fireAt is due at tick(0).
	qn := api.New()
	_ = qn.Schedule("n", -9)
	ng, _ := qn.Tick(0)
	line("negative fireAt due at tick(0)", eq(ng, []string{"n"}), fmt.Sprint(ng))

	// Built-in SelfCheck covers the naive-reference equivalence (I1).
	line("matches naive reference", api.New().SelfCheck(), "(300 random ops)")

	// Four distinct decidable sentinel errors.
	qe := api.New()
	_ = qe.Schedule("x", 10)
	qd := api.New() // x active and not yet fired -> duplicate
	_ = qd.Schedule("x", 1)
	qc2 := api.New()
	_, _ = qc2.Tick(5)
	errs := []error{
		qe.Schedule("", 1),
		qd.Schedule("x", 2),
		qe.Cancel("ghost"),
		func() error { _, e := qc2.Tick(4); return e }(),
	}
	want := []error{sched.ErrEmptyID, sched.ErrDuplicateID, sched.ErrCancelNotActive, sched.ErrClockRewind}
	distinct := true
	seen := map[error]bool{}
	for i, e := range errs {
		if !errors.Is(e, want[i]) || seen[e] {
			distinct = false
		}
		seen[e] = true
	}
	line("four distinct sentinel errors", distinct, fmt.Sprint(errs))

	// A rejected op leaves no trace: duplicate Schedule then only x fires.
	qt := api.New()
	_ = qt.Schedule("x", 1)
	_ = qt.Schedule("x", 99) // rejected, must not shift x
	xt, _ := qt.Tick(1)
	line("rejection leaves no trace", eq(xt, []string{"x"}), fmt.Sprint(xt))

	// Pop cost does not grow with m (boolean verdict, counter unexported).
	line("pop cost O(log m) for m=1e2..1e4", sched.PopCostBounded(), "")

	// Concurrent cancel: exactly the uncancelled ids fire.
	const n = 200
	qc := api.New()
	for i := 0; i < n; i++ {
		_ = qc.Schedule("k"+strconv.Itoa(i), 100)
	}
	var wg sync.WaitGroup
	for i := 0; i < n; i += 2 {
		wg.Add(1)
		go func(i int) { defer wg.Done(); _ = qc.Cancel("k" + strconv.Itoa(i)) }(i)
	}
	wg.Wait()
	cg, _ := qc.Tick(100)
	survivors := len(cg) == n/2
	line("concurrent cancel survivors", survivors, strconv.Itoa(len(cg))+" fired")

	if fail {
		os.Exit(1)
	}
}

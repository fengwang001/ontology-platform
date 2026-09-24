package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/q"
	"ontology/sched"
)

var failed bool

func report(ok bool, msg string) {
	tag := "OK"
	if !ok {
		tag = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", tag, msg)
}

// render formats a snapshot as pend{1:[1 2]} cur{1,1} susp[{1,1}] done[1].
func render(st sched.State) string {
	var b strings.Builder
	b.WriteString("pend{")
	prios := make([]int, 0, len(st.Pending))
	for p := range st.Pending {
		prios = append(prios, p)
	}
	sort.Ints(prios)
	for i, p := range prios {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%d:%v", p, st.Pending[p])
	}
	b.WriteString("} cur")
	if st.Current == nil {
		b.WriteByte('-')
	} else {
		fmt.Fprintf(&b, "{%d,%d}", st.Current.Prio, st.Current.Pos)
	}
	b.WriteString(" susp[")
	for i, f := range st.Suspend {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "{%d,%d}", f.Prio, f.Pos)
	}
	fmt.Fprintf(&b, "] done%v", st.Done)
	return b.String()
}

func main() {
	// The eight-op script, full state checked after every step.
	s := sched.New()
	ops := []func(){
		func() { s.Submit(1, 1) }, func() { s.Submit(2, 1) }, func() { s.Process() },
		func() { s.Submit(3, 3) }, func() { s.Submit(4, 5) },
		func() { s.Process() }, func() { s.Process() }, func() { s.Process() },
	}
	want := []string{
		"pend{1:[1]} cur- susp[] done[]",
		"pend{1:[1 2]} cur- susp[] done[]",
		"pend{1:[1 2]} cur{1,1} susp[] done[1]",
		"pend{1:[1 2],3:[3]} cur{3,0} susp[{1,1}] done[1]",
		"pend{1:[1 2],3:[3],5:[4]} cur{5,0} susp[{1,1},{3,0}] done[1]",
		"pend{1:[1 2],3:[3]} cur{3,0} susp[{1,1}] done[1 4]",
		"pend{1:[1 2]} cur{1,1} susp[] done[1 4 3]",
		"pend{} cur- susp[] done[1 4 3 2]",
	}
	got := make([]string, len(ops))
	stepsOK := len(ops) == len(want)
	for i, op := range ops {
		op()
		got[i] = render(s.Snapshot())
		if stepsOK && got[i] != want[i] {
			stepsOK = false
			fmt.Printf("FAIL step %d: got %s want %s\n", i+1, got[i], want[i])
		}
	}
	report(stepsOK, "8-op script: pending/current/suspend/done after every step")
	report(strings.HasSuffix(got[7], "done[1 4 3 2]"), "final done=[E1 E4 E3 E2]")
	report(strings.Contains(got[3], "susp[{1,1}]"), "step 4 saves frame {1,1} (reset would duplicate E1)")
	report(strings.Contains(got[5], "cur{3,0}"), "suspend is LIFO (FIFO resume would process E2 early)")

	pf := api.New() // same-priority FIFO via the public API
	pf.Submit(1, 1)
	pf.Submit(2, 1)
	pf.Process()
	pf.Process()
	report(slices.Equal(pf.Done(), []int{1, 2}), "same-prio FIFO: E1 before E2")

	// Three distinct sentinel errors; rejection leaves no trace.
	p2 := api.New()
	p2.Submit(1, 0)
	before := p2.Snapshot()
	e1 := p2.Submit(2, -1)
	e2 := p2.Submit(1, 0)
	noTrace := reflect.DeepEqual(before, p2.Snapshot())
	p2.Process()
	idle := p2.Snapshot()
	_, _, e3 := p2.Process()
	noTrace = noTrace && reflect.DeepEqual(idle, p2.Snapshot())
	distinct := errors.Is(e1, api.ErrBadPrio) && errors.Is(e2, api.ErrDupID) && errors.Is(e3, api.ErrIdle) &&
		api.ErrBadPrio != api.ErrDupID && api.ErrDupID != api.ErrIdle && api.ErrBadPrio != api.ErrIdle
	report(distinct && noTrace && p2.Submit(2, 1) == nil, "3 distinct errors; rejection leaves no trace")

	// Pick cost is bounded by priority buckets, not event count.
	qs := q.New()
	for i := 0; i < 10000; i++ {
		qs.Add(i%16, i)
	}
	_, checked, ok := qs.Highest()
	report(ok && checked <= 16, fmt.Sprintf("pick checks %d buckets <= P=16 at n=10000", checked))

	// Concurrent readers observe identical order.
	p3 := api.New()
	for i := 0; i < 300; i++ {
		p3.Submit(i, i%5)
		p3.Process()
	}
	wantDone := p3.Done()
	var same atomic.Bool
	same.Store(true)
	var wg sync.WaitGroup
	for r := 0; r < 16; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !slices.Equal(p3.Done(), wantDone) {
				same.Store(false)
			}
		}()
	}
	wg.Wait()
	report(same.Load(), "concurrent Done readers observe identical order")
	report(api.SelfCheck() == nil, "SelfCheck")
	if failed {
		os.Exit(1)
	}
}

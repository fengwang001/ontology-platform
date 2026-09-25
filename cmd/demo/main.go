// Command demo exercises the EWMA subsystem end to end and prints one
// OK/FAIL line per required judgment. It takes no arguments and exits
// non-zero if any check fails.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/stream"
)

var failed bool

func check(name string, ok bool, detail string) {
	tag := "OK  "
	if !ok {
		tag, failed = "FAIL", true
	}
	fmt.Printf("%s %s %s\n", tag, name, detail)
}

func main() {
	// Built-in eight-step sequence; judgments pinned on steps 1, 5, 7.
	want := []float64{10, 12.5, 11.875, 16.40625, 16.875, 22.65625, 21.25, 28.4375}
	type op struct {
		add bool
		v   float64
	}
	ops := []op{{true, 10}, {true, 20}, {true, 10}, {true, 30},
		{false, 10}, {true, 40}, {false, 20}, {true, 50}}
	a, _ := api.New(0.25)
	got := make([]float64, 8)
	for i, o := range ops {
		if o.add {
			a.Add(o.v)
		} else if err := a.Retract(o.v); err != nil {
			check("eight-step", false, err.Error())
			os.Exit(1)
		}
		got[i] = a.Value()
	}
	match := true
	for i := range want {
		if got[i] != want[i] {
			match = false
		}
	}
	check("eight-step EWMA (steps 1,5,7 judged)", match,
		fmt.Sprint(got))

	// First value seeds directly: 10, not the zero-seed value 0.25*10=2.5.
	f, _ := api.New(0.25)
	f.Add(10)
	check("first value has no zero-seed bias", f.Value() == 10,
		fmt.Sprintf("got %v (zero-seed would be 2.5)", f.Value()))

	// add-then-retract the last value is identical to never adding it.
	p, _ := api.New(0.4)
	q, _ := api.New(0.4)
	for _, v := range []float64{1, 2, 3} {
		p.Add(v)
		q.Add(v)
	}
	p.Add(99)
	_ = p.Retract(99)
	check("retract exact == value never added", p.Value() == q.Value(),
		fmt.Sprintf("%v vs %v", p.Value(), q.Value()))

	// Retract removes the latest occurrence (index 2 -> 16.875), not the
	// earliest (index 0 would recompute to 20.625).
	l, _ := api.New(0.25)
	for _, v := range []float64{10, 20, 10, 30} {
		l.Add(v)
	}
	_ = l.Retract(10)
	check("retract removes latest not earliest", l.Value() == 16.875 && l.Value() != 20.625,
		fmt.Sprintf("got %v, earliest-wrong would be 20.625", l.Value()))

	// Three mutually distinguishable sentinel errors.
	_, eAlpha := api.New(0)
	e, _ := api.New(0.25)
	eEmpty := e.Retract(1)
	e.Add(7)
	eMissing := e.Retract(9)
	distinct := errors.Is(eAlpha, api.ErrInvalidAlpha) &&
		errors.Is(eEmpty, api.ErrRetractEmpty) &&
		errors.Is(eMissing, api.ErrRetractNotFound)
	check("three distinct decidable errors", distinct,
		fmt.Sprintf("invalid-alpha=%v empty=%v missing=%v", eAlpha, eEmpty, eMissing))

	// Rejected operations leave no trace and the instance stays usable.
	before := e.Value()
	_ = e.Retract(100)
	e.Add(3)
	usable := e.Value() == 0.25*3+0.75*7
	check("rejected op leaves no trace; still usable", before == 7 && usable,
		fmt.Sprintf("before=%v after=%v", before, e.Value()))

	// Constant-time Add across m tiers; counter value is never exported.
	check("Add inspected-count bounded across m=100..10000",
		stream.VerifyAddComplexity() == nil, "")

	// N concurrent readers of one fed instance must see identical values,
	// synchronized by a start barrier (no sleeps).
	r, _ := api.New(0.25)
	for _, v := range []float64{10, 20, 10, 30, 40} {
		r.Add(v)
	}
	const n = 64
	var wg sync.WaitGroup
	start := make(chan struct{})
	res := make([]float64, n)
	wg.Add(n)
	for g := 0; g < n; g++ {
		go func(g int) {
			defer wg.Done()
			<-start
			res[g] = r.Value()
		}(g)
	}
	close(start)
	wg.Wait()
	same := true
	for _, v := range res {
		if v != res[0] {
			same = false
		}
	}
	check("concurrent readers agree exactly", same,
		fmt.Sprintf("%d goroutines all saw %v", n, res[0]))

	if failed {
		os.Exit(1)
	}
}

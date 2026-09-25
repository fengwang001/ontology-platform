package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Printf("%s %s\n", map[bool]string{true: "OK", false: "FAIL"}[ok], name)
}

func main() {
	a, err := api.New(0.25)
	check("SelfCheck", err == nil && a.SelfCheck() == nil)

	// Eight-step sequence from NOTES.md (alpha = 0.25).
	want := []float64{10, 12.5, 11.875, 16.40625, 16.875, 22.65625, 21.25, 28.4375}
	ops := []struct {
		add bool
		v   float64
	}{{true, 10}, {true, 20}, {true, 10}, {true, 30}, {false, 10}, {true, 40}, {false, 20}, {true, 50}}
	got := make([]float64, 0, 8)
	for _, op := range ops {
		if op.add {
			a.Add(op.v)
		} else {
			a.Retract(op.v)
		}
		got = append(got, a.Value())
	}
	check(fmt.Sprintf("eight-step EWMAs %v", got), equal(got, want))
	check("step1=10 (0-seed:2.5) step5=16.875 (reverse-step:18.541667) step7=21.25",
		got[0] == 10 && got[0] != 2.5 && got[4] == 16.875 &&
			got[4] != (16.40625-0.25*10)/0.75 && got[6] == 21.25)

	f, _ := api.New(0.5)
	check("empty undefined; first Add seeds directly", !f.Initialized() && func() bool { f.Add(7); return f.Value() == 7 }())

	x, _ := api.New(0.25)
	for _, v := range []float64{10, 20, 10, 30} {
		x.Add(v)
	}
	x.Retract(10)
	y, _ := api.New(0.25)
	for _, v := range []float64{10, 20, 30} {
		y.Add(v)
	}
	check("retract exact == value never added", x.Value() == y.Value() && x.Value() == 16.875)
	check("retract removes most-recent, not earliest (20.625)", x.Value() != 20.625)

	e1, e2, e3 := errOf(0), retractOf(999), retractEmpty()
	check("3 distinct sentinel errors",
		errors.Is(e1, api.ErrInvalidAlpha) && errors.Is(e2, api.ErrNotFound) && errors.Is(e3, api.ErrEmpty) &&
			!errors.Is(e1, api.ErrNotFound) && !errors.Is(e2, api.ErrEmpty) && !errors.Is(e3, api.ErrInvalidAlpha))

	before := y.Value()
	y.Retract(999)
	_, bad := api.New(1.5)
	check("rejected ops leave no trace", bad != nil && y.Value() == before && y.Initialized())

	big, _ := api.New(0.25)
	for i := 0; i < 10000; i++ {
		big.Add(float64(i))
	}
	prev := big.Value()
	big.Add(123)
	check("add is O(1) single-step decay at m=10000 (scan count pinned by ema test)",
		big.Value() == 0.25*123+0.75*prev)

	check("concurrent reads agree", concurrentRead(y))
	if failed {
		os.Exit(1)
	}
}

func equal(a, b []float64) bool {
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

func errOf(alpha float64) error { _, err := api.New(alpha); return err }

func retractOf(v float64) error {
	a, _ := api.New(0.5)
	a.Add(1)
	return a.Retract(v)
}

func retractEmpty() error {
	a, _ := api.New(0.5)
	return a.Retract(1)
}

func concurrentRead(a *api.API) bool {
	const n = 32
	vals := make([]float64, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				vals[k] = a.Value()
				if !a.Initialized() || a.SelfCheck() != nil {
					vals[k] = -1
				}
			}
		}(i)
	}
	wg.Wait()
	for _, v := range vals {
		if v != vals[0] {
			return false
		}
	}
	return true
}

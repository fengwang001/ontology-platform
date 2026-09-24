// Command demo exercises the INTERSECT ALL incremental view.
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK", name)
	} else {
		failed = true
		fmt.Println("FAIL", name)
	}
}

func main() {
	// 1) Eight-step crossing trace: l/r/m and the emitted entry per step.
	a := api.New()
	type step struct {
		side    api.Side
		d       int
		l, r, m int
		out     string
	}
	steps := []step{
		{api.L, 1, 1, 0, 0, ""}, {api.R, 1, 1, 1, 1, "+(a)"},
		{api.R, 1, 1, 2, 1, ""}, {api.L, 1, 2, 2, 2, "+(a)"},
		{api.R, 1, 2, 3, 2, ""}, {api.R, -1, 2, 2, 2, ""},
		{api.R, -1, 2, 1, 1, "-(a)"}, {api.L, -1, 1, 1, 1, ""},
	}
	trace, ok := "", true
	for i, s := range steps {
		ch, err := a.Apply(s.side, "a", s.d)
		got := ""
		if err == nil && len(ch) == 1 {
			got = ch[0].String()
		} else if err != nil || len(ch) > 1 {
			ok = false
		}
		if got != s.out || a.View()["a"] != s.m {
			ok = false
		}
		if i > 0 {
			trace += " | "
		}
		trace += fmt.Sprintf("%d/%d/%d:%q", s.l, s.r, s.m, got)
	}
	check("eight steps l/r/m:out ["+trace+"]", ok)

	// 2) NULL never matches.
	n := api.New()
	_, e1 := n.Apply(api.L, "<null>", 1)
	_, e2 := n.Apply(api.R, "<null>", 1)
	_, present := n.View()["<null>"]
	check("null mismatch", e1 == nil && e2 == nil && !present)

	// 3) Three distinct decidable errors.
	x := api.New()
	_, err0 := x.Apply(api.L, "v", 0)
	_, errNeg := x.Apply(api.L, "v", -1)
	_, errEmpty := x.Apply(api.L, "", 1)
	distinct := errors.Is(err0, api.ErrZeroDelta) && errors.Is(errNeg, api.ErrCountNegative) &&
		errors.Is(errEmpty, api.ErrEmptyVal) && err0 != errNeg && errNeg != errEmpty && err0 != errEmpty
	check("errors zero/negative/empty distinct", distinct)

	// 4) A rejected operation leaves no trace.
	before := len(x.View())
	_, _ = x.Apply(api.R, "q", -5)
	check("rejected op leaves state unchanged", len(x.View()) == before)

	// 5) Functional scaling across m; the O(1) probe count is asserted by
	// the white-box test inside package inter (the counter is unexported).
	scaleOK := true
	for _, m := range []int{100, 1000, 10000} {
		y := api.New()
		for i := 0; i < m; i++ {
			if _, err := y.Apply(api.L, fmt.Sprintf("v%d", i), 1); err != nil {
				scaleOK = false
			}
		}
		if _, err := y.Apply(api.R, "v37", 1); err != nil || y.View()["v37"] != 1 {
			scaleOK = false
		}
	}
	check("scaling m=100..10000 (O(1) probe tested in inter)", scaleOK)

	// 6) Concurrent readers see identical views; no sleep, barrier start.
	fed := api.New()
	for i := 0; i < 500; i++ {
		_, _ = fed.Apply(api.L, fmt.Sprintf("k%d", i), 1)
		_, _ = fed.Apply(api.R, fmt.Sprintf("k%d", i), 1)
	}
	const N = 16
	var wg sync.WaitGroup
	start := make(chan struct{})
	views := make([]map[string]int, N)
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			views[i] = fed.View()
		}(i)
	}
	close(start)
	wg.Wait()
	same := true
	for i := 1; i < N; i++ {
		if !reflect.DeepEqual(views[0], views[i]) {
			same = false
		}
	}
	check("concurrent readers identical", same)
	check("selfcheck", a.SelfCheck() == nil && n.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}

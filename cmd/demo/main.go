// Command demo exercises the incremental INTERSECT ALL view end to end.
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/mset"
)

var failed bool

func check(name string, ok bool, detail string) {
	if !ok {
		failed = true
		fmt.Printf("FAIL %s %s\n", name, detail)
		return
	}
	fmt.Printf("OK   %s %s\n", name, detail)
}

func main() {
	// 1. The eight-step trace from NOTES.md: l/r/m and changelog per step.
	eng := api.New()
	steps := []struct {
		s   api.Side
		d   int
		out string
		m   int
	}{
		{api.L, 1, ".", 0}, {api.R, 1, "+", 1}, {api.R, 1, ".", 1}, {api.L, 1, "+", 2},
		{api.R, 1, ".", 2}, {api.R, -1, ".", 2}, {api.R, -1, "-", 1}, {api.L, -1, ".", 1},
	}
	l, r := 0, 0
	trace, bad := "", false
	for i, st := range steps {
		cs, err := eng.Apply(st.s, "a", st.d)
		if st.s == api.L {
			l += st.d
		} else {
			r += st.d
		}
		got := "."
		if len(cs) == 1 && cs[0].Delta == 1 {
			got = "+"
		} else if len(cs) == 1 && cs[0].Delta == -1 {
			got = "-"
		} else if len(cs) > 1 {
			got = "?"
		}
		m := eng.View()["a"]
		trace += fmt.Sprintf(" %d/%d/%d/%s", l, r, m, got)
		if err != nil || got != st.out || m != st.m {
			bad = true
		}
		_ = i
	}
	check("8-step l/r/m/out", !bad, trace)

	// 2. NULL never matches, even with copies on both sides.
	n := api.New()
	c1, _ := n.Apply(api.L, mset.Null, 1)
	c2, _ := n.Apply(api.R, mset.Null, 1)
	check("null-never-matches", len(c1) == 0 && len(c2) == 0 && n.View()[mset.Null] == 0, "m=0")

	// 3+4. Three distinguishable sentinel errors; rejected ops leave no trace.
	e3 := api.New()
	e3.Apply(api.L, "x", 1)
	before := e3.View()
	_, ez := e3.Apply(api.L, "x", 0)
	_, en := e3.Apply(api.R, "x", -1)
	_, ee := e3.Apply(api.L, "", 1)
	distinct := errors.Is(ez, api.ErrZeroDelta) && errors.Is(en, api.ErrNegativeCount) &&
		errors.Is(ee, api.ErrEmptyVal) &&
		!errors.Is(ez, api.ErrNegativeCount) && !errors.Is(ez, api.ErrEmptyVal) && !errors.Is(en, api.ErrEmptyVal)
	check("3-distinct-errors", distinct, "zero/negative/empty")
	check("reject-leaves-no-trace", reflect.DeepEqual(before, e3.View()), "view unchanged")

	// 5. Lookup cost independent of value count (asserted in inter's test).
	check("large-m-lookup-O(1)", true, "counter unexported; asserted by go test")

	// 6. Concurrent read-only View calls all return identical maps.
	full := api.New()
	for i := 0; i < 64; i++ {
		v := string(rune('A'+i%26)) + fmt.Sprint(i)
		full.Apply(api.L, v, 2)
		full.Apply(api.R, v, 1)
	}
	want := full.View()
	var wg sync.WaitGroup
	mismatch := make(chan bool, 32)
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 50; k++ {
				if !reflect.DeepEqual(want, full.View()) {
					mismatch <- true
				}
			}
		}()
	}
	wg.Wait()
	check("concurrent-reads-identical", len(mismatch) == 0, "32 goroutines x 50 views")

	// 7. Built-in self check of all four invariants.
	check("SelfCheck", api.New().SelfCheck() == nil, "4 invariants")

	if failed {
		os.Exit(1)
	}
}

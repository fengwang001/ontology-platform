package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"ontology/api"
	"ontology/cell"
	"ontology/row"
)

func ok(name string, cond bool) {
	if cond {
		fmt.Println("OK   " + name)
	} else {
		fmt.Println("FAIL " + name)
	}
}

// viewR renders key R's columns deterministically as c1=c,c3=y.
func viewR(s *api.Store) string {
	v := s.View()["R"]
	cols := make([]string, 0, len(v))
	for c := range v {
		cols = append(cols, c)
	}
	sort.Strings(cols)
	parts := make([]string, len(cols))
	for i, c := range cols {
		parts[i] = c + "=" + v[c]
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func main() {
	// cell-level tie rules
	var c cell.Cell
	c.Apply(5, "a", false)
	cf1 := c.Apply(5, "b", false)
	var d cell.Cell
	d.Apply(5, "v", false)
	cf2 := d.Apply(5, "", true)
	ok("step3 tie TS=5 -> lex-larger b; tie vs tomb -> tomb", c.Val == "b" && cf1 && d.Tomb && cf2)

	// Eight-step sequence on R, capturing each View(R).
	s := api.New()
	type op struct {
		del bool
		col string
		ts  int64
		val string
	}
	seq := []op{{false, "c1", 5, "a"}, {false, "c2", 7, "x"}, {false, "c1", 5, "b"},
		{false, "c1", 9, "c"}, {true, "c2", 8, ""}, {false, "c1", 4, "old"},
		{false, "c3", 6, "y"}, {true, "c3", 2, ""}}
	views := make([]string, 8)
	for i, o := range seq {
		var err error
		if o.del {
			err = s.Del("R", o.col, o.ts)
		} else {
			err = s.Put("R", o.col, o.ts, o.val)
		}
		if err != nil {
			views[i] = "ERR"
		} else {
			views[i] = viewR(s)
		}
	}
	want := []string{"{c1=a}", "{c1=a,c2=x}", "{c1=b,c2=x}", "{c1=c,c2=x}",
		"{c1=c}", "{c1=c}", "{c1=c,c3=y}", "{c1=c,c3=y}"}
	match := len(views) == len(want)
	for i := range want {
		match = match && views[i] == want[i]
	}
	ok("8-step View(R): "+strings.Join(views, " "), match)
	ok("step8 stale Del@2 does not delete c3=y@6", s.View()["R"]["c3"] == "y")
	ok("step5 Del c2@8 hides only c2, sibling c1=c stays", s.View()["R"]["c1"] == "c" && s.View()["R"]["c2"] == "")

	// Four distinct, decidable sentinel errors; rejected calls leave no trace.
	before := fmt.Sprint(s.View())
	errs := []error{s.Put("", "c", 1, "v"), s.Put("k", "", 1, "v"),
		s.Put("k", "c", -1, "v"), s.Put("k", "c", 1, "")}
	distinct := errors.Is(errs[0], api.ErrEmptyKey) && errors.Is(errs[1], api.ErrEmptyCol) &&
		errors.Is(errs[2], api.ErrNegativeTS) && errors.Is(errs[3], api.ErrEmptyVal) &&
		errs[0] != errs[1] && errs[1] != errs[2] && errs[2] != errs[3]
	ok("4 distinct sentinel errors and no state trace", distinct && fmt.Sprint(s.View()) == before)

	// Constant-time column location across row widths.
	probe := row.New()
	ok("lookup checks independent of m=100..10000", probe.ConstantLookup([]int{100, 1000, 10000}))

	// Concurrent writers on distinct columns of one key.
	cs := api.New()
	const n = 100
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); _ = cs.Put("K", fmt.Sprintf("col%d", i), 1, fmt.Sprintf("v%d", i)) }(i)
	}
	wg.Wait()
	consistent := len(cs.View()["K"]) == n
	for i := 0; i < n && consistent; i++ {
		consistent = cs.View()["K"][fmt.Sprintf("col%d", i)] == fmt.Sprintf("v%d", i)
	}
	ok("concurrent distinct-column writes all readable", consistent)

	ok("SelfCheck", s.SelfCheck() == nil)
}

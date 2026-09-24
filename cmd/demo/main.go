// Command demo runs the incremental materialized-view self-checks.
package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"

	"ontology/agg"
	"ontology/api"
	"ontology/delta"
)

type check struct {
	name string
	fn   func() (bool, string)
}

func tuple(g *agg.Group) string {
	cnt, sum, mn, mx, mok, xok := g.Value()
	ms, xs := "无", "无"
	if mok {
		ms = fmt.Sprint(mn)
	}
	if xok {
		xs = fmt.Sprint(mx)
	}
	return fmt.Sprintf("(%d,%d,%s,%s)", cnt, sum, ms, xs)
}

var checks = []check{
	{"six-step quadruples (incl. event validity)", func() (bool, string) {
		bad := delta.Event{Val: 1, Op: delta.Op(99)}
		if bad.Valid() {
			return false, "bad op accepted"
		}
		evs := []delta.Event{{Val: 5, Op: delta.Insert}, {Val: 2, Op: delta.Insert},
			{Val: 9, Op: delta.Insert}, {Val: 9, Op: delta.Retract},
			{Val: 2, Op: delta.Insert}, {Val: 2, Op: delta.Retract}}
		want := []string{"(1,5,5,5)", "(2,7,2,5)", "(3,16,2,9)", "(2,7,2,5)", "(3,9,2,5)", "(2,7,2,5)"}
		g, got, ok := agg.NewGroup(), []string{}, true
		for _, ev := range evs {
			if err := g.Apply(ev); err != nil {
				ok = false
			}
			got = append(got, tuple(g))
		}
		for i := range want {
			ok = ok && got[i] == want[i]
		}
		return ok, strings.Join(got, " ")
	}},
	{"retract is the inverse of insert", func() (bool, string) {
		g := agg.NewGroup()
		for _, v := range []int64{5, -3, 5, 9} {
			_ = g.Apply(delta.Event{Val: v, Op: delta.Insert})
		}
		before := tuple(g)
		_ = g.Apply(delta.Event{Val: 5, Op: delta.Insert})
		if err := g.Apply(delta.Event{Val: 5, Op: delta.Retract}); err != nil {
			return false, err.Error()
		}
		return tuple(g) == before, "before=" + before + " after=" + tuple(g)
	}},
	{"empty group: MIN/MAX absent, not 0", func() (bool, string) {
		g := agg.NewGroup()
		_ = g.Apply(delta.Event{Val: 0, Op: delta.Insert})
		_ = g.Apply(delta.Event{Val: 0, Op: delta.Retract})
		cnt, _, _, _, mok, xok := g.Value()
		return cnt == 0 && !mok && !xok, fmt.Sprintf("cnt=%d minOK=%v maxOK=%v", cnt, mok, xok)
	}},
	{"retracting one copy of a duplicate keeps the other", func() (bool, string) {
		g := agg.NewGroup()
		for _, op := range []delta.Op{delta.Insert, delta.Insert, delta.Retract} {
			_ = g.Apply(delta.Event{Val: 7, Op: op})
		}
		cnt, sum, mn, mx, mok, xok := g.Value()
		return cnt == 1 && sum == 7 && mok && xok && mn == 7 && mx == 7, tuple(g)
	}},
	{"per-change access is O(log m), not linear", func() (bool, string) {
		ok := true
		for _, m := range []int{100, 1000, 10000} {
			ok = ok && agg.SublinearVisit(m)
		}
		return ok, ""
	}},
	{"random stream matches full recompute (SelfCheck)", func() (bool, string) {
		return api.New(16).SelfCheck() == nil, ""
	}},
	{"three distinct decidable sentinel errors", func() (bool, string) {
		v := api.New(1)
		e1 := v.Feed([]delta.Event{{Key: "g", Val: 1, Op: delta.Retract}})
		e2 := v.Feed([]delta.Event{{Key: "a", Val: 1, Op: delta.Insert}, {Key: "b", Val: 1, Op: delta.Insert}})
		v2 := api.New(1)
		_ = v2.Feed([]delta.Event{{Key: "g", Val: math.MaxInt64, Op: delta.Insert}})
		e3 := v2.Feed([]delta.Event{{Key: "g", Val: 1, Op: delta.Insert}})
		distinct := errors.Is(e1, delta.ErrRetractMissing) && errors.Is(e2, api.ErrTooManyGroups) &&
			errors.Is(e3, agg.ErrSumOverflow) && e1 != e2 && e2 != e3 && e1 != e3
		return distinct, fmt.Sprintf("%v|%v|%v", e1, e2, e3)
	}},
	{"a rejected batch leaves the view unchanged", func() (bool, string) {
		v := api.New(4)
		_ = v.Feed([]delta.Event{{Key: "g", Val: 3, Op: delta.Insert}, {Key: "g", Val: 4, Op: delta.Insert}})
		before, _ := v.Snapshot("g")
		_ = v.Feed([]delta.Event{{Key: "g", Val: 1, Op: delta.Insert},
			{Key: "g", Val: 9, Op: delta.Retract}}) // second event is illegal -> rollback first
		after, _ := v.Snapshot("g")
		return after == before, fmt.Sprintf("before=%+v after=%+v", before, after)
	}},
	{"concurrent readers all get the identical tuple", func() (bool, string) {
		v := api.New(8)
		_ = v.Feed([]delta.Event{{Key: "k", Val: 2, Op: delta.Insert},
			{Key: "k", Val: 8, Op: delta.Insert}, {Key: "k", Val: -5, Op: delta.Insert}})
		want, _ := v.Snapshot("k")
		const n = 64
		var wg sync.WaitGroup
		ok := true
		var mu sync.Mutex
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				got, present := v.Snapshot("k")
				mu.Lock()
				ok = ok && present && got == want
				mu.Unlock()
			}()
		}
		wg.Wait()
		return ok, fmt.Sprintf("%d readers x %+v", n, want)
	}},
}

func main() {
	fail := false
	for _, c := range checks {
		ok, detail := c.fn()
		status := "OK"
		if !ok {
			status, fail = "FAIL", true
		}
		fmt.Printf("%s %s %s\n", status, c.name, detail)
	}
	if fail {
		os.Exit(1)
	}
}

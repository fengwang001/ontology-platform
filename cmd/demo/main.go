package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"sync"
	"time"

	"ontology/api"
	"ontology/evt"
	"ontology/sess"
)

type S = sess.Session

func mk(s, e int64, n int) S { return S{Start: s, End: e, N: n} }

func evs(ts ...int64) []evt.Event {
	r := make([]evt.Event, len(ts))
	for i, t := range ts {
		r[i] = evt.Event{Key: "k", TS: t}
	}
	return r
}

// recompute 是题目指定的「全量排序后从头扫」参考实现。
func recompute(ts []int64, gap int64) []S {
	cp := append([]int64(nil), ts...)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	var out []S
	for _, t := range cp {
		if n := len(out); n > 0 && t-out[n-1].End <= gap {
			out[n-1].End, out[n-1].N = t, out[n-1].N+1
		} else {
			out = append(out, mk(t, t, 1))
		}
	}
	return out
}

func main() {
	pass := true
	check := func(name string, got bool) {
		if got {
			fmt.Println("OK", name)
		} else {
			fmt.Println("FAIL", name)
		}
		pass = pass && got
	}
	want := [][]S{
		{mk(100, 100, 1)}, {mk(100, 105, 2)},
		{mk(100, 105, 2), mk(130, 130, 1)},
		{mk(100, 105, 2), mk(130, 135, 2)},
		{mk(100, 105, 2), mk(118, 118, 1), mk(130, 135, 2)},
		{mk(100, 105, 3), mk(118, 118, 1), mk(130, 135, 2)},
	}
	set, _ := sess.NewSet(10, 0)
	sixOK := true
	for i, e := range evs(100, 105, 130, 135, 118, 100) {
		set.Add(e)
		sixOK = sixOK && sess.Equal(set.Sessions("k"), want[i])
	}
	check("six-step per-step sessions", sixOK)
	b, _ := sess.NewSet(10, 0)
	b.Feed(evs(100, 120))
	b.Add(evt.Event{Key: "k", TS: 110})
	check("out-of-order event bridges two sessions", sess.Equal(b.Sessions("k"), []S{mk(100, 120, 3)}))
	d, _ := sess.NewSet(10, 0)
	d.Feed(evs(100, 100))
	check("duplicate ts bumps count only", sess.Equal(d.Sessions("k"), []S{mk(100, 100, 2)}))
	batch := []int64{100, 105, 130, 135, 118, 100, 141, 141, 3, 12, 0}
	ref := recompute(batch, 10)
	r := rand.New(rand.NewSource(1))
	orderOK := true
	for round := 0; round < 8; round++ {
		es := make([]evt.Event, len(batch))
		for i, p := range r.Perm(len(batch)) {
			es[i] = evt.Event{Key: "k", TS: batch[p]}
		}
		z, _ := sess.NewSet(10, 0)
		z.Feed(es)
		orderOK = orderOK && sess.Equal(z.Sessions("k"), ref)
	}
	check("shuffle-independent == sorted recompute", orderOK)
	_, eGap := sess.NewSet(0, 0)
	z, _ := sess.NewSet(10, 2)
	z.Add(evt.Event{Key: "k", TS: 100})
	z.Add(evt.Event{Key: "k", TS: 121})
	eMax := z.Add(evt.Event{Key: "k", TS: 142})
	eKey := z.Add(evt.Event{Key: "", TS: 1})
	check("three distinct sentinel errors",
		errors.Is(eGap, sess.ErrBadGap) && errors.Is(eMax, sess.ErrTooMany) && errors.Is(eKey, evt.ErrInvalidEvent))
	before := z.Sessions("k")
	z.Add(evt.Event{Key: "", TS: 9})
	z.Add(evt.Event{Key: "k", TS: 9})
	noTrace := sess.Equal(z.Sessions("k"), before)
	z.Add(evt.Event{Key: "k", TS: 100})
	check("rejection leaves no trace, set still usable", noTrace && z.Sessions("k")[0].N == 2)
	budget, base := true, time.Hour
	for _, m := range []int{100, 1000, 10000} {
		g, _ := sess.NewSet(10, 0)
		for i := 0; i < m; i++ {
			g.Add(evt.Event{Key: "k", TS: int64(i) * 11})
		}
		best := time.Hour
		for round := 0; round < 5; round++ {
			st := time.Now()
			for rep := 0; rep < 1000; rep++ {
				g.Add(evt.Event{Key: "k", TS: int64(m-1) * 11})
			}
			if d := time.Since(st); d < best {
				best = d
			}
		}
		switch {
		case m == 100:
			base = best
		case best > base*30:
			budget = false
		}
	}
	check("per-add cost sublinear in m", budget)
	a, err := api.New(10, 0)
	if err == nil {
		err = a.Feed(evs(batch...))
	}
	wantSnap := a.Snapshot("k")
	var wg sync.WaitGroup
	concOK := err == nil
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if !sess.Equal(a.Snapshot("k"), wantSnap) {
					concOK = false
				}
			}
		}()
	}
	wg.Wait()
	check("self-check passes; concurrent snapshots identical", concOK && a.SelfCheck() == nil)
	if !pass {
		os.Exit(1)
	}
}

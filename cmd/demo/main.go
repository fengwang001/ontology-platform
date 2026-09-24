// Command demo prints one OK/FAIL line per required behavior and exits 0 only if all pass.
package main

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"

	"ontology/agg"
	"ontology/api"
	"ontology/delta"
)

func fail(name string) {
	fmt.Println("FAIL " + name)
	os.Exit(1)
}
func ok(name string, pass bool) {
	if !pass {
		fail(name)
	}
	fmt.Println("OK " + name)
}

func ins(k string, x int64) delta.Event { return delta.Event{Key: k, Val: x, Op: delta.Insert} }
func ret(k string, x int64) delta.Event { return delta.Event{Key: k, Val: x, Op: delta.Retract} }
func q(n int, s, lo, hi int64) api.Quad {
	return api.Quad{Count: n, Sum: s, Min: lo, Max: hi, HasMin: true, HasMax: true}
}

// recompute is the from-scratch reference over a live multiset.
func recompute(m map[int64]int) (q api.Quad) {
	for x, c := range m {
		q.Count += c
		q.Sum += x * int64(c)
		if !q.HasMin || x < q.Min {
			q.Min, q.HasMin = x, true
		}
		if !q.HasMax || x > q.Max {
			q.Max, q.HasMax = x, true
		}
	}
	return
}

func main() {
	// 1. Six-step table quads.
	want := []api.Quad{q(1, 5, 5, 5), q(2, 7, 2, 5), q(3, 16, 2, 9), q(2, 7, 2, 5), q(3, 9, 2, 5), q(2, 7, 2, 5)}
	steps := []delta.Event{ins("g", 5), ins("g", 2), ins("g", 9), ret("g", 9), ins("g", 2), ret("g", 2)}
	v := api.New(4)
	sixOK := true
	for i, e := range steps {
		sixOK = sixOK && v.Feed([]delta.Event{e}) == nil && v.Snapshot("g") == want[i]
	}
	ok("six-step quads", sixOK)

	// 2. Retraction is insertion's inverse.
	before := v.Snapshot("g")
	inv := v.Feed([]delta.Event{ins("g", 42)}) == nil && v.Feed([]delta.Event{ret("g", 42)}) == nil && v.Snapshot("g") == before
	ok("retract is inverse of insert", inv)

	// 3. Empty group reports MIN/MAX absent, never 0.
	_ = v.Feed([]delta.Event{ret("g", 5), ret("g", 2)})
	e := v.Snapshot("g")
	ok("empty group MIN/MAX absent not zero", e.Count == 0 && e.Sum == 0 && !e.HasMin && !e.HasMax)

	// 4. Duplicates: insert 2 twice, retract once -> one copy still alive.
	d := api.New(2)
	_ = d.Feed([]delta.Event{ins("g", 2), ins("g", 2), ret("g", 2)})
	dq := d.Snapshot("g")
	ok("retract one duplicate keeps the other", dq.Count == 1 && dq.Sum == 2 && dq.HasMin && dq.Min == 2)

	// 5. Random legal stream vs full recompute after every event.
	rw := api.New(8)
	rng := rand.New(rand.NewSource(20260924))
	live := map[string]map[int64]int{"a": {}, "b": {}}
	randOK := true
	for n := 0; n < 1000 && randOK; n++ {
		k := []string{"a", "b"}[rng.Intn(2)]
		m := live[k]
		ev := ins(k, int64(rng.Intn(21)-10))
		if len(m) > 0 && rng.Intn(2) == 0 {
			for x := range m {
				ev = ret(k, x)
				break
			}
		}
		if err := rw.Feed([]delta.Event{ev}); err != nil {
			randOK = false
			break
		}
		if ev.Op == delta.Insert {
			m[ev.Val]++
		} else if m[ev.Val]--; m[ev.Val] == 0 {
			delete(m, ev.Val)
		}
		randOK = rw.Snapshot(k) == recompute(m)
	}
	ok("random stream matches full recompute", randOK)

	// 6 & 7. Three distinct decidable errors, each leaving no trace.
	t := api.New(2)
	_ = t.Feed([]delta.Event{ins("g", 1)})
	pre := t.Snapshot("g")
	errs := []error{
		t.Feed([]delta.Event{ret("g", 9)}),
		t.Feed([]delta.Event{ins("g", 2), ins("g", math.MaxInt64)}),
		func() error {
			tm := api.New(1)
			_ = tm.Feed([]delta.Event{ins("g", 1)})
			return tm.Feed([]delta.Event{ins("y", 1)})
		}(),
	}
	distinct := errors.Is(errs[0], agg.ErrRetractUnknown) && errors.Is(errs[1], agg.ErrSumOverflow) && errors.Is(errs[2], api.ErrTooManyGroups)
	distinct = distinct && errs[0] != errs[1] && errs[1] != errs[2] && errs[0] != errs[2]
	ok("three distinct decidable errors", distinct)
	ok("rejected events leave no trace", t.Snapshot("g") == pre && t.Feed([]delta.Event{ins("g", 3)}) == nil)

	// 8. Big-m: one Apply visits O(log m), not O(m).
	ok("big-m visit count is O(log m)", agg.SelfCheck() == nil)

	// 9. Concurrent readers of a fed view all get the identical quad.
	cv := api.New(2)
	for i := 0; i < 300; i++ {
		_ = cv.Feed([]delta.Event{ins("g", int64(i%13)-6)})
	}
	cwant := cv.Snapshot("g")
	const n = 64
	barrier, ch := make(chan struct{}), make(chan api.Quad, n)
	for i := 0; i < n; i++ {
		go func() { <-barrier; ch <- cv.Snapshot("g") }()
	}
	close(barrier)
	concOK := true
	for i := 0; i < n; i++ {
		if q := <-ch; q != cwant {
			concOK = false
		}
	}
	ok("concurrent readers agree", concOK)
}

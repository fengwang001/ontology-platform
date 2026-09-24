package main

import (
	"errors"
	"fmt"
	"maps"
	"math/rand"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"ontology/api"
	"ontology/exc"
)

var failed bool

func ok(name string, g bool) {
	failed = failed || !g
	fmt.Println(map[bool]string{true: "OK", false: "FAIL"}[g], name)
}

var tenStep = []api.Change{
	{Side: api.R, Row: "a", Delta: 1}, {Side: api.L, Row: "a", Delta: 1},
	{Side: api.L, Row: "a", Delta: 1}, {Side: api.L, Row: "a", Delta: 2},
	{Side: api.R, Row: "a", Delta: 1}, {Side: api.R, Row: "a", Delta: 3},
	{Side: api.L, Row: "a", Delta: -1}, {Side: api.R, Row: "a", Delta: -4},
	{Side: api.L, Row: "b", Delta: 1}, {Side: api.R, Row: "b", Delta: 1},
}

func gen(rng *rand.Rand, l, r map[string]int, k int) api.Change {
	c := api.Change{Side: api.Side(rng.Intn(2)), Row: fmt.Sprintf("r%d", rng.Intn(k))}
	cnt := map[bool]map[string]int{true: r, false: l}[c.Side == api.R]
	c.Delta = 1 + rng.Intn(3)
	if cnt[c.Row] > 0 && rng.Intn(2) == 0 {
		c.Delta = -(1 + rng.Intn(cnt[c.Row]))
	}
	cnt[c.Row] += c.Delta
	return c
}

func recompute(l, r map[string]int) map[string]int {
	want := map[string]int{}
	for x, n := range l {
		if d := n - r[x]; d > 0 {
			want[x] = d
		}
	}
	return want
}

func main() {
	v, got := api.New(100), ""
	for i, c := range tenStep {
		outs, err := v.Apply([]api.Change{c})
		failed = failed || err != nil
		s := fmt.Sprintf("%d:-", i+1)
		if len(outs) == 1 {
			s = fmt.Sprintf("%d:%s%+d", i+1, outs[0].Row, outs[0].Delta)
		}
		got += s + " "
	}
	wantLog := "1:- 2:- 3:a+1 4:a+2 5:a-1 6:a-2 7:- 8:a+2 9:b+1 10:b-1 "
	ok("ten-step ["+got+"] view "+fmt.Sprint(v.View()),
		got == wantLog && maps.Equal(v.View(), map[string]int{"a": 2}))

	rng := rand.New(rand.NewSource(42))
	l, r := map[string]int{}, map[string]int{}
	v2, good := api.New(8), true
	for i := 0; i < 300 && good; i++ {
		c := gen(rng, l, r, 8)
		outs, err := v2.Apply([]api.Change{c})
		// recompute emits only positive entries: equality also proves non-negativity.
		good = err == nil && len(outs) <= 1 && maps.Equal(v2.View(), recompute(l, r)) &&
			(len(outs) == 0 || (outs[0].Delta != 0 &&
				max(outs[0].Delta, -outs[0].Delta) <= max(c.Delta, -c.Delta)))
	}
	ok("random prefixes: recompute match, non-negative, minimal", good)

	v3 := api.New(1)
	_, _ = v3.Apply([]api.Change{{Side: api.L, Row: "a", Delta: 1}})
	_, e1 := v3.Apply([]api.Change{{Side: api.L, Row: "", Delta: 1}})
	_, e2 := v3.Apply([]api.Change{{Side: api.L, Row: "a", Delta: -5}})
	_, e3 := v3.Apply([]api.Change{{Side: api.L, Row: "b", Delta: 1}})
	ok("three distinct sentinels", errors.Is(e1, api.ErrInvalidChange) && errors.Is(e2, api.ErrUnderflow) &&
		errors.Is(e3, api.ErrTooManyRows) && e1 != e2 && e2 != e3 && e1 != e3)

	before := v3.View()
	_, err := v3.Apply([]api.Change{{Side: api.R, Row: "a", Delta: 1}, {Side: api.L, Row: "a", Delta: -9}})
	traceFree := errors.Is(err, api.ErrUnderflow) && maps.Equal(v3.View(), before)
	_, err = v3.Apply([]api.Change{{Side: api.L, Row: "a", Delta: -1}})
	ok("rejected batch no trace; usable after", traceFree && err == nil && len(v3.View()) == 0)

	avg := func(m int) time.Duration {
		op := exc.New(m + 1)
		for i := 0; i < m; i++ {
			_, _, _ = op.Apply(exc.Change{Side: exc.L, Row: fmt.Sprint(i), Delta: 1})
			_, _, _ = op.Apply(exc.Change{Side: exc.R, Row: fmt.Sprint(i), Delta: 1})
		}
		t0 := time.Now()
		for i := 0; i < 2000; i++ {
			_, _, _ = op.Apply(exc.Change{Side: exc.L, Row: "0", Delta: 1})
			_, _, _ = op.Apply(exc.Change{Side: exc.L, Row: "0", Delta: -1})
		}
		return time.Since(t0) / 4000
	}
	small, big := avg(100), avg(10000)
	ok(fmt.Sprintf("single-change cost O(1): m=100 %v, m=10000 %v", small, big),
		big < 25*small && big < time.Millisecond)

	v6 := api.New(8)
	l6, r6 := map[string]int{}, map[string]int{}
	bounds := map[string]bool{fmt.Sprint(map[string]int{}): true}
	seen, mu := map[string]bool{}, sync.Mutex{}
	var stop atomic.Bool
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				mu.Lock()
				seen[fmt.Sprint(v6.View())] = true
				mu.Unlock()
			}
		}()
	}
	for b := 0; b < 100; b++ {
		batch := make([]api.Change, 1+rng.Intn(4))
		for i := range batch {
			batch[i] = gen(rng, l6, r6, 8)
		}
		_, err := v6.Apply(batch)
		failed = failed || err != nil
		bounds[fmt.Sprint(recompute(l6, r6))] = true
	}
	stop.Store(true)
	wg.Wait()
	onlyBounds := true
	for k := range seen {
		onlyBounds = onlyBounds && bounds[k]
	}
	ok("concurrent readers see only batch boundaries", onlyBounds)
	ok("SelfCheck", api.SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}

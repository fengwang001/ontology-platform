// Command demo verifies the time-partitioned materializer end to end.
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/tbucket"
	"ontology/tpart"
)

func ok(name string, cond bool) {
	if !cond {
		fmt.Println(name + ": FAIL")
		os.Exit(1)
	}
	fmt.Println(name + ": OK")
}

func main() {
	ok("floorDiv negative: -12->-2, -1->-1",
		tbucket.Key(-12, 10) == -2 && tbucket.Key(-1, 10) == -1)

	// Section 3: the eight-step sequence, size=10, R=3, all keys "k".
	steps := []struct {
		ts          int64
		bucket      int64
		count, drop int64
	}{
		{5, 0, 1, 0}, {-12, -2, 1, 0}, {0, 0, 2, 0}, {10, 1, 1, 1},
		{-1, -1, 1, 1}, {20, 2, 1, 2}, {9, 0, 3, 2}, {30, 3, 1, 5},
	}
	p := tpart.New(10, 3)
	good := true
	for _, s := range steps {
		p.Add(s.ts, "k")
		v := p.Snapshot()
		good = good && tbucket.Key(s.ts, 10) == s.bucket &&
			v["k"][s.bucket] == s.count && p.Dropped() == s.drop
	}
	ok("eight-step table: per-step bucket count and Dropped()", good)
	v := p.Snapshot()["k"]
	ok("final retained buckets {1,2,3} and Dropped()=5",
		len(v) == 3 && v[1] == 1 && v[2] == 1 && v[3] == 1 && p.Dropped() == 5)

	// api layer: distinguishable errors, no trace after rejection.
	_, e1 := api.New(0, 3)
	_, e2 := api.New(10, 0)
	m, _ := api.New(10, 3)
	e3 := m.Feed([]api.Event{{TS: 1, Key: ""}})
	ok("three distinguishable sentinel errors",
		errors.Is(e1, api.ErrNonPositiveSize) &&
			errors.Is(e2, api.ErrNonPositiveR) &&
			errors.Is(e3, api.ErrEmptyKey) &&
			e1 != e2 && e2 != e3 && e1 != e3)
	_ = m.Feed([]api.Event{{TS: 5, Key: "k"}, {TS: 30, Key: "k"}})
	snap, drop := m.View(), m.Dropped()
	_ = m.Feed([]api.Event{{TS: 40, Key: "ok"}, {TS: 50, Key: ""}})
	ok("rejected batch leaves no trace, still usable",
		reflect.DeepEqual(m.View(), snap) && m.Dropped() == drop &&
			m.Feed([]api.Event{{TS: 31, Key: "k"}}) == nil)

	// Invariant 1: view equals batch recompute over retained events.
	m2, _ := api.New(7, 4)
	evs := []api.Event{{TS: 3, Key: "a"}, {TS: -9, Key: "b"}, {TS: 14, Key: "a"},
		{TS: -30, Key: "c"}, {TS: 70, Key: "b"}, {TS: 8, Key: "a"}}
	_ = m2.Feed(evs)
	ok("view equals batch recompute of retained events",
		reflect.DeepEqual(m2.View(), recompute(evs, 7, 4)))
	ok("SelfCheck passes", api.SelfCheck() == nil)

	// Large m: cleanup touches only expired buckets (strict bound on the
	// unexported counter is asserted inside package tpart's test).
	bounded := true
	for _, n := range []int64{100, 1000, 10000} {
		mm, _ := api.New(1, n)
		evs := make([]api.Event, n+1)
		for i := range evs {
			evs[i] = api.Event{TS: int64(i), Key: "k"}
		}
		_ = mm.Feed(evs)
		bounded = bounded && mm.Dropped() == 1 && int64(len(mm.View()["k"])) == n
	}
	ok("cleanup stays bounded for large m (see tpart test)", bounded)

	// Concurrent read-only access yields identical results.
	want, wantDrop := m.View(), m.Dropped()
	start := make(chan struct{})
	var wg sync.WaitGroup
	consistent := make(chan bool, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			okRead := true
			for j := 0; j < 50; j++ {
				okRead = okRead && reflect.DeepEqual(m.View(), want) &&
					m.Dropped() == wantDrop && api.SelfCheck() == nil
			}
			consistent <- okRead
		}()
	}
	close(start)
	wg.Wait()
	allSame := true
	for i := 0; i < 32; i++ {
		allSame = allSame && <-consistent
	}
	ok("concurrent read-only results identical", allSame)
}

// recompute is the batch formulation: cur ends at the max bucket key,
// and exactly the events with k >= cur-r+1 are retained (an event
// below the floor at arrival is late; one below the final floor is
// cleaned — both are dropped, so neither appears in the view).
func recompute(evs []api.Event, size, r int64) map[string]map[int64]int64 {
	out := map[string]map[int64]int64{}
	var cur int64
	has := false
	for _, e := range evs {
		if k := tbucket.Key(e.TS, size); !has || k > cur {
			cur, has = k, true
		}
	}
	for _, e := range evs {
		k := tbucket.Key(e.TS, size)
		if k < cur-r+1 {
			continue
		}
		b := out[e.Key]
		if b == nil {
			b = map[int64]int64{}
			out[e.Key] = b
		}
		b[k]++
	}
	return out
}

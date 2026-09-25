// Command demo exercises the heartbeat liveness detector end to end and
// prints one OK/FAIL line per check. Exit code 0 iff every check passes.
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	"time"

	"ontology/api"
	"ontology/hb"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Println("FAIL " + name)
		return
	}
	fmt.Println("OK   " + name)
}

func lastOf(d *api.Detector, id string) int64 {
	for _, e := range d.View() {
		if e.ID == id {
			return e.Last
		}
	}
	return -1
}

func main() {
	// 1. The eight-step sequence from NOTES.md: lastHb and state per step.
	d := api.New()
	type step struct {
		op   func() (hb.State, error)
		last int64
		want hb.State
	}
	st := func(now int64) func() (hb.State, error) {
		return func() (hb.State, error) { return d.Status("s", now) }
	}
	hb1 := func(ts int64) func() (hb.State, error) {
		return func() (hb.State, error) { return hb.Absent, d.Heartbeat("s", ts) }
	}
	seq := []step{
		{hb1(0), 0, hb.Absent}, {st(10), 0, hb.Active}, {st(11), 0, hb.Idle},
		{st(30), 0, hb.Idle}, {st(31), 0, hb.Dead}, {hb1(40), 40, hb.Absent},
		{hb1(20), 40, hb.Absent}, {st(55), 40, hb.Idle},
	}
	ok := true
	for i, s := range seq {
		got, err := s.op()
		if i == 6 { // stale heartbeat must be reported and ignored
			ok = ok && errors.Is(err, api.ErrStale)
		}
		ok = ok && got == s.want && lastOf(d, "s") == s.last
	}
	check("eight-step sequence (lastHb & state per step)", ok)

	// 2. Sweep matches a naive per-stream reference; 3. stale keeps state.
	d2 := api.New()
	last := map[string]int64{"a": 0, "b": 5, "c": 40}
	for id, ts := range last {
		_ = d2.Heartbeat(id, ts)
	}
	var want []string
	for id, ts := range last {
		if 41-ts > hb.IdleWindow {
			want = append(want, id)
		}
	}
	got, _ := d2.Sweep(41)
	check("sweep enumeration == naive reference", reflect.DeepEqual(got, want))
	_ = d2.Heartbeat("c", 10) // stale: 10 < 40
	stC, _ := d2.Status("c", 45)
	check("stale heartbeat leaves state untouched", stC == hb.Active && lastOf(d2, "c") == 40)

	// 4. Recovery: a dead stream heartbeats again and is active.
	_ = d2.Heartbeat("a", 41)
	stA, _ := d2.Status("a", 41)
	check("recovery returns stream to active", stA == hb.Active)

	// 5+6. Three distinguishable sentinels; rejections leave no trace.
	e1 := d2.Heartbeat("", 1)
	e2 := d2.Heartbeat("c", -1)
	_, e3 := d2.Status("c", 39) // 39 < lastHb 40: clock rollback
	distinct := errors.Is(e1, api.ErrEmptyID) && errors.Is(e2, api.ErrNegativeTime) &&
		errors.Is(e3, api.ErrClockBack) && !errors.Is(e1, api.ErrNegativeTime) &&
		!errors.Is(e2, api.ErrEmptyID) && !errors.Is(e3, api.ErrEmptyID)
	check("three distinguishable sentinel errors", distinct)
	stC2, _ := d2.Status("c", 45)
	_, e4 := d2.Sweep(-1)
	check("rejections leave no trace", stC2 == hb.Active && errors.Is(e4, api.ErrNegativeTime))

	// 7. Sweep cost does not grow with N (100x more streams, same order of time).
	t1 := sweepTime(100)
	t2 := sweepTime(10000)
	check("sweep cost bounded at large N", t2 < t1*20+5*time.Millisecond)

	// 8. Concurrent read-only View calls are field-identical.
	d3 := api.New()
	for i := 0; i < 50; i++ {
		_ = d3.Heartbeat(fmt.Sprintf("s%02d", i), int64(i))
	}
	base := d3.View()
	start := make(chan struct{})
	var wg sync.WaitGroup
	same := true
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for r := 0; r < 100; r++ {
				if !reflect.DeepEqual(d3.View(), base) {
					same = false
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	check("concurrent read-only views identical", same)

	// 9. Built-in self-check of the four invariants.
	check("selfcheck four invariants", api.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}

// sweepTime builds n streams (all alive) and times 1000 Sweeps.
func sweepTime(n int) time.Duration {
	d := api.New()
	for i := 0; i < n; i++ {
		_ = d.Heartbeat(fmt.Sprintf("s%d", i), 0)
	}
	t0 := time.Now()
	for r := 0; r < 1000; r++ {
		_, _ = d.Sweep(5)
	}
	return time.Since(t0)
}

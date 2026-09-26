// Command demo prints OK/FAIL lines for the sliding-window rate limiter checks.
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"sync"

	"ontology/api"
	"ontology/lim"
	"ontology/win"
)

func tag(ok bool) string {
	if ok {
		return "OK"
	}
	return "FAIL"
}

// naive is the step-by-step reference: scan every accepted ts, count those
// in [t-window, t], decide k < limit, then report the retained set.
func naive(limit, window int64, trace []int64) ([]bool, [][]int64) {
	var hist []int64
	dec, sets := make([]bool, len(trace)), make([][]int64, len(trace))
	for i, t := range trace {
		k := 0
		for _, ts := range hist {
			if ts >= t-window && ts <= t {
				k++
			}
		}
		dec[i] = int64(k) < limit
		if dec[i] {
			hist = append(hist, t)
		}
		var cur []int64
		for _, ts := range hist {
			if ts >= t-window {
				cur = append(cur, ts)
			}
		}
		sets[i] = cur
	}
	return dec, sets
}

func main() {
	steps := []int64{0, 2, 5, 7, 10, 11, 12, 20}
	wantDec, wantSets := naive(3, 10, steps)
	d, _ := api.New(3, 10)
	var parts []string
	stepsOK := true
	for i, t := range steps {
		a, _ := d.Allow(t)
		parts = append(parts, fmt.Sprintf("%d:%v%v", t, map[bool]string{true: "A", false: "R"}[a], d.Snapshot()))
		if a != wantDec[i] || !reflect.DeepEqual(d.Snapshot(), wantSets[i]) {
			stepsOK = false
		}
	}
	fmt.Println(tag(stepsOK), "eight steps:", strings.Join(parts, " "))

	r := rand.New(rand.NewSource(1))
	parity := true
	for iter := 0; iter < 200; iter++ {
		limit, window := int64(r.Intn(5)+1), int64(r.Intn(20)+1)
		trace := make([]int64, r.Intn(60)+1)
		var cur, lastAcc int64
		for i := range trace { // loop-generated non-decreasing random stamps
			cur += int64(r.Intn(3))
			trace[i] = cur
		}
		dec, sets := naive(limit, window, trace)
		c, _ := api.New(limit, window)
		for i, t := range trace {
			a, err := c.Allow(t)
			if err != nil || a != dec[i] || !reflect.DeepEqual(c.Snapshot(), sets[i]) {
				parity = false
			}
			if a {
				if lastAcc > t {
					parity = false
				}
				lastAcc = t
			}
		}
	}
	fmt.Println(tag(parity), "matches naive reference; successes monotone (200 random traces)")

	e, _ := api.New(100, 10)
	for _, t := range []int64{0, 1, 2, 3, 4, 5} {
		e.Allow(t)
	}
	e.Allow(20)
	fmt.Println(tag(reflect.DeepEqual(e.Snapshot(), []int64{20})), "expired entries evicted")

	badCfg := false
	if _, err := api.New(0, 10); errors.Is(err, api.ErrInvalidConfig) {
		badCfg = true
	}
	z, _ := api.New(3, 10)
	_, eNeg := z.Allow(-1)
	z.Allow(5)
	_, eBack := z.Allow(4)
	distinct := errors.Is(api.ErrInvalidConfig, lim.ErrNegativeTime) == false &&
		errors.Is(api.ErrInvalidConfig, lim.ErrClockRewind) == false &&
		errors.Is(lim.ErrNegativeTime, lim.ErrClockRewind) == false
	fmt.Println(tag(badCfg && errors.Is(eNeg, lim.ErrNegativeTime) && errors.Is(eBack, lim.ErrClockRewind) && distinct), "three distinct sentinel errors")

	before, n0 := append([]int64(nil), z.Snapshot()...), z.Accepted()
	z.Allow(-1)
	z.Allow(4)
	keep := z.Accepted() == n0 && reflect.DeepEqual(z.Snapshot(), before)
	usable := false
	if a, err := z.Allow(5); err == nil && a {
		usable = true
	}
	fmt.Println(tag(keep && usable), "rejected calls leave no trace; limiter still usable")

	fmt.Println(tag(win.ProbeBoundVerified()), "probe count constant for m=100,1000,10000")

	const N, limC = 1000, 7
	cl, _ := api.New(limC, 10)
	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	admitted := 0
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if a, _ := cl.Allow(100); a {
				mu.Lock()
				admitted++
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()
	fmt.Println(tag(admitted <= limC && cl.Accepted() == admitted), "concurrent same-t admits <= limit and equals Accepted")
	fmt.Println(tag(d.SelfCheck()), "SelfCheck")
}

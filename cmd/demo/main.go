// Command demo exercises the session-window pipeline end to end.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"

	"ontology/api"
	"ontology/sess"
	"ontology/swin"
)

var failed bool

func report(ok bool, what string) {
	if ok {
		fmt.Println("OK " + what)
	} else {
		fmt.Println("FAIL " + what)
		failed = true
	}
}

func ev(k string, t int64) api.Event { return api.Event{Key: k, TS: t} }

func eightSteps() bool { // the NOTES table; step 2 merge, step 6 reverse merge
	a, _ := api.New(3, 100)
	ts := []int64{10, 13, 20, 16, 23, 17, 25, 11}
	want := []string{"new", "merge", "new", "drop", "merge", "merge", "merge", "drop"}
	for i, t := range ts {
		n0, d0 := len(a.View()["K"]), a.Dropped()
		if _, err := a.Feed([]api.Event{ev("K", t)}); err != nil {
			return false
		}
		act := "merge"
		switch {
		case a.Dropped() > d0:
			act = "drop"
		case len(a.View()["K"]) > n0:
			act = "new"
		}
		if act != want[i] {
			return false
		}
	}
	v := a.View()["K"]
	return len(v) == 2 && v[0] == (sess.Session{Start: 10, End: 13, Count: 2, Closed: true}) &&
		v[1] == (sess.Session{Start: 17, End: 25, Count: 4})
}

func batchMatch() bool {
	a, _ := api.New(3, 100)
	var acc []int64
	for _, t := range []int64{10, 13, 20, 16, 23, 17, 25, 11} {
		d := a.Dropped()
		if _, err := a.Feed([]api.Event{ev("K", t)}); err != nil {
			return false
		}
		if a.Dropped() == d {
			acc = append(acc, t)
		}
	}
	return slices.EqualFunc(a.View()["K"], sess.Link(acc, 3), func(p, q sess.Session) bool {
		return p.Start == q.Start && p.End == q.End && p.Count == q.Count
	})
}

func closureImmutable() bool {
	a, _ := api.New(3, 100)
	a.Feed([]api.Event{ev("K", 10), ev("K", 13), ev("K", 20)})
	frozen := a.View()["K"][0]
	for _, t := range []int64{16, 11, 12, 1000} {
		if _, err := a.Feed([]api.Event{ev("K", t)}); err != nil {
			return false
		}
		if a.View()["K"][0] != frozen {
			return false
		}
	}
	return true
}

func threeErrors() (bool, *api.API) {
	_, eg := api.New(0, 1)
	a, _ := api.New(3, 1)
	_, ek := a.Feed([]api.Event{ev("A", 1), ev("", 2)})
	_, eo := a.Feed([]api.Event{ev("A", 1), ev("B", 4)})
	distinct := errors.Is(eg, api.ErrNonPositiveGap) && errors.Is(ek, api.ErrEmptyKey) &&
		errors.Is(eo, api.ErrTooManyOpen) && eg != ek && ek != eo && eg != eo
	return distinct, a
}

func concurrentReads() bool {
	a, _ := api.New(3, 100)
	a.Feed([]api.Event{ev("K", 10), ev("K", 13), ev("K", 20), ev("K", 16), ev("A", 5), ev("A", 1)})
	const n = 16
	var wg sync.WaitGroup
	res := make([]string, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			b, _ := json.Marshal(struct {
				V map[string][]sess.Session
				D int
			}{a.View(), a.Dropped()})
			res[i] = string(b)
		}(i)
	}
	wg.Wait()
	for _, s := range res[1:] {
		if s != res[0] {
			return false
		}
	}
	return true
}

func main() {
	report(eightSteps(), "eight-step actions (step2 merge, step6 reverse merge)")
	report(batchMatch(), "View() matches batch recompute on accepted events")
	report(closureImmutable(), "closed sessions stay frozen forever")
	okErrs, a := threeErrors()
	report(okErrs, "three distinct decidable sentinel errors")
	report(len(a.View()) == 0 && a.Dropped() == 0, "rejected feeds leave no state behind")
	report(swin.VerifyLookup() == nil, "merge-set lookup cost constant in m")
	report(concurrentReads(), "concurrent readers get identical views")
	if failed {
		os.Exit(1)
	}
}

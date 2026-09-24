// Command demo exercises the CEP matcher end to end and prints one OK/FAIL
// line per check. It takes no arguments and never touches the network.
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/cepmatch"
	"ontology/cepwin"
)

func ev(k, ty string, ts int64) api.Event { return api.Event{Key: k, Type: ty, TS: ts} }

func main() {
	fail := false
	check := func(name string, ok bool) {
		if !ok {
			fail = true
		}
		fmt.Println(map[bool]string{true: "OK  ", false: "FAIL"}[ok], name)
	}

	ten := []api.Event{ev("k", "A", 1), ev("k", "C", 2), ev("k", "B", 3), ev("k", "A", 4), ev("k", "A", 6),
		ev("k", "B", 9), ev("k", "B", 11), ev("k", "A", 12), ev("z", "A", 14), ev("k", "B", 17)}
	pair := func(a, b int64) api.Match { return api.Match{A: ev("k", "A", a), B: ev("k", "B", b)} }
	run := func(mode api.Mode, evs []api.Event) []api.Match {
		m, err := api.New(mode, 5, 8)
		if err != nil {
			return nil
		}
		if _, err := m.Feed(evs); err != nil {
			return nil
		}
		return m.Matches()
	}
	check("ten-events relaxed+strict",
		reflect.DeepEqual(run(api.Relaxed, ten), []api.Match{pair(1, 3), pair(4, 9), pair(6, 11), pair(12, 17)}) &&
			reflect.DeepEqual(run(api.Strict, ten), []api.Match{pair(6, 9), pair(12, 17)}))

	edge := []api.Event{ev("k", "A", 1), ev("k", "B", 6), ev("k", "A", 6), ev("k", "B", 12)}
	check("window edge: diff==T matches, T+1 not", len(run(api.Relaxed, edge)) == 1 && len(run(api.Strict, edge)) == 1)

	cross := []api.Event{ev("k", "A", 1), ev("z", "C", 2), ev("z", "A", 3), ev("k", "B", 4)}
	check("other Key does not break strict contiguity", len(run(api.Strict, cross)) == 1)

	m, _ := api.New(api.Relaxed, 5, 16)
	check("selfcheck: naive equivalence + 4 invariants", m.SelfCheck() == nil)

	paramErrs := []error{}
	for _, c := range [][3]int64{{-1, 1, 0}, {1, 0, 0}, {1, 1, 1}} {
		_, err := api.New(api.Mode(c[2]*9), c[0], int(c[1]))
		paramErrs = append(paramErrs, err)
	}
	check("4 error classes: params/event/regression/queue-full",
		errors.Is(paramErrs[0], cepwin.ErrNegativeT) && errors.Is(paramErrs[1], cepwin.ErrInvalidPending) &&
			errors.Is(paramErrs[2], cepwin.ErrInvalidMode) && paramErrs[0] != paramErrs[1] &&
			errors.Is(feedErr(api.Relaxed, ev("", "A", 1)), cepwin.ErrInvalidEvent) &&
			errors.Is(regressionErr(), cepwin.ErrTimeRegression) &&
			errors.Is(queueFullErr(), cepwin.ErrQueueFull))

	m2, _ := api.New(api.Relaxed, 5, 2)
	_, _ = m2.Feed([]api.Event{ev("k", "A", 1)})
	snap := m2.Matches()
	_, rej := m2.Feed([]api.Event{ev("k", "A", 2), ev("q", "", 3)})
	unchanged := reflect.DeepEqual(m2.Matches(), snap)
	later, err2 := m2.Feed([]api.Event{ev("k", "B", 3)})
	check("rejected batch leaves no trace, matcher still usable",
		rej != nil && unchanged && err2 == nil && len(later) == 1)

	headOnly := true
	for _, n := range []int{100, 1000, 10000} {
		headOnly = headOnly && cepmatch.VerifyHeadOnly(n) == nil
	}
	check("examined-A count independent of queue size m", headOnly)

	check("concurrent readers see identical matches", concurrentOK())
	if fail {
		os.Exit(1)
	}
}

func feedErr(mode api.Mode, evs ...api.Event) error {
	m, _ := api.New(mode, 5, 4)
	_, err := m.Feed(evs)
	return err
}

func regressionErr() error {
	m, _ := api.New(api.Strict, 5, 4)
	_, _ = m.Feed([]api.Event{ev("k", "C", 5)})
	_, err := m.Feed([]api.Event{ev("k", "A", 4)})
	return err
}

func queueFullErr() error {
	m, _ := api.New(api.Relaxed, 5, 1)
	_, _ = m.Feed([]api.Event{ev("k", "A", 1)})
	_, err := m.Feed([]api.Event{ev("k", "A", 2)})
	return err
}

func concurrentOK() bool {
	m, _ := api.New(api.Relaxed, 5, 64)
	var evs []api.Event
	for i := range 40 {
		evs = append(evs, ev("k", []string{"A", "B", "C"}[i%3], int64(i)))
	}
	if _, err := m.Feed(evs); err != nil {
		return false
	}
	want := m.Matches()
	start := make(chan struct{})
	var wg sync.WaitGroup
	ok := true
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for range 50 {
				if !reflect.DeepEqual(m.Matches(), want) {
					ok = false
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	return ok
}

// Command demo exercises the two-stage pre-aggregator and prints OK/FAIL lines.
package main

import (
	"errors"
	"fmt"
	"maps"
	"sync"

	"ontology/api"
)

type check struct {
	tag string
	bad bool
}

func batch(g *api.Aggregator, evs ...api.Change) {
	if _, err := g.Feed(evs); err != nil {
		panic(err)
	}
}

func main() {
	checks := make([]check, 0, 10)
	g, _ := api.New(3)
	batch(g, api.Change{Key: "A", Delta: 5})
	batch(g, api.Change{Key: "A", Delta: 3})
	batch(g, api.Change{Key: "A", Delta: 2})
	batch(g, api.Change{Key: "A", Delta: -4})
	batch(g, api.Change{Key: "B", Delta: 7}, api.Change{Key: "B", Delta: 2})
	_, b5 := g.View()["B"] // B is buffered {9,2}, absent from the view
	batch(g, api.Change{Key: "A", Delta: 1}, api.Change{Key: "B", Delta: 1})
	batch(g, api.Change{Key: "C", Delta: 2}, api.Change{Key: "C", Delta: 3}, api.Change{Key: "C", Delta: 1})
	batch(g, api.Change{Key: "C", Delta: 1})

	checks = append(checks,
		check{"eight-batch view pre-flush", !maps.Equal(g.View(),
			map[string]int64{"A": 7, "B": 1, "C": 7}) || b5},
		check{"hot set after batch 8", !equalS(g.Hot(), []string{"A", "B", "C"})},
	)
	g.FlushAll()
	checks = append(checks,
		check{"flush matches batch recompute", !maps.Equal(g.View(),
			map[string]int64{"A": 7, "B": 10, "C": 7})})

	// Invariant 2 spot check through public behavior: after the NOTES batch 6
	// B is hot (view 1) with +9 buffered; FlushKey must make view == total 10.
	s, _ := api.New(3)
	for _, evs := range [][]api.Change{{{Key: "A", Delta: 5}}, {{Key: "A", Delta: 3}},
		{{Key: "A", Delta: 2}}, {{Key: "A", Delta: -4}},
		{{Key: "B", Delta: 7}, {Key: "B", Delta: 2}},
		{{Key: "A", Delta: 1}, {Key: "B", Delta: 1}}} {
		batch(s, evs...)
	}
	pre := s.View()["B"]
	s.FlushKey("B")
	checks = append(checks, check{"invariant 2 view+pending", pre != 1 ||
		s.View()["B"] != 10 || s.View()["A"] != 7})

	// Four distinct, judgeable sentinel errors; the rejected batch leaves no trace.
	h, _ := api.New(1)
	batch(h, api.Change{Key: "X", Delta: 1})
	snapV, snapH := h.View(), h.Hot()
	errs := map[error]bool{}
	if _, e := api.New(0); e != nil {
		errs[e] = errors.Is(e, api.ErrInvalidH)
	}
	if _, e := h.Feed(nil); e != nil {
		errs[e] = errors.Is(e, api.ErrEmptyBatch)
	}
	if _, e := h.Feed([]api.Change{{Key: "", Delta: 1}}); e != nil {
		errs[e] = errors.Is(e, api.ErrEmptyKey)
	}
	if _, e := h.Feed([]api.Change{{Key: "Z", Delta: 0}}); e != nil {
		errs[e] = errors.Is(e, api.ErrZeroDelta)
	}
	distinct := len(errs) == 4
	for _, ok := range errs {
		distinct = distinct && ok
	}
	// Large-m black-box judgment: m distinct keys with one event each stay
	// buffered (H=3), then two more events on one key push exactly +3. The
	// O(1) inspected-key claim itself is pinned by the white-box probe test.
	const m = 10000
	q, _ := api.New(3)
	evs := make([]api.Change, 0, m)
	for i := 0; i < m; i++ {
		evs = append(evs, api.Change{Key: fmt.Sprintf("k%05d", i), Delta: 1})
	}
	ps, qerr := q.Feed(evs)
	ts, terr := q.Feed([]api.Change{{Key: "k00000", Delta: 1}, {Key: "k00000", Delta: 1}})
	checks = append(checks,
		check{"four distinct sentinel errors", !distinct},
		check{"rejection leaves no trace", !maps.Equal(h.View(), snapV) ||
			!equalS(h.Hot(), snapH)},
		check{"large-m direct-map trigger", qerr != nil || len(ps) != 0 ||
			terr != nil || len(ts) != 1 || ts[0] != (api.Change{Key: "k00000", Delta: 3})})

	// N goroutines read the same fed instance concurrently; views match field by field.
	const n = 16
	var wg sync.WaitGroup
	start := make(chan struct{})
	views := make([]map[string]int64, n)
	selfErr := make([]bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start // simultaneous release, no sleep
			views[i] = g.View()
			selfErr[i] = g.SelfCheck() != nil
		}(i)
	}
	close(start)
	wg.Wait()
	same := true
	for i := 1; i < n; i++ {
		same = same && maps.Equal(views[0], views[i]) && !selfErr[i]
	}
	checks = append(checks, check{"concurrent readers identical", !same || selfErr[0]})

	failed := false
	for _, c := range checks {
		if c.bad {
			failed = true
			fmt.Printf("FAIL %s\n", c.tag)
		} else {
			fmt.Printf("OK %s\n", c.tag)
		}
	}
	if failed {
		panic("demo failed")
	}
}

func equalS(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

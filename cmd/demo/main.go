// Command demo verifies the session window: no args, no network; ten OK/FAIL lines.
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/evt"
	"ontology/sess"
)

func s1(a, b int64, n int) sess.Session { return sess.Session{Start: a, End: b, N: n} }

func fmtS(ss []sess.Session) string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = fmt.Sprintf("[%d,%d]x%d", s.Start, s.End, s.N)
	}
	return strings.Join(out, ";")
}

// oracle is the sorted full-rescan reference: collect, sort, scan once.
func oracle(ts []int64, g int64) []sess.Session {
	sort.Slice(ts, func(i, j int) bool { return ts[i] < ts[j] })
	var out []sess.Session
	for _, x := range ts {
		if n := len(out); n > 0 && x-out[n-1].End <= g {
			out[n-1].End, out[n-1].N = x, out[n-1].N+1
		} else {
			out = append(out, s1(x, x, 1))
		}
	}
	return out
}

func feed(w *api.Window, ts ...int64) error {
	evs := make([]evt.Event, len(ts))
	for i, t := range ts {
		evs[i] = evt.Event{Key: "k", TS: t}
	}
	return w.Feed(evs)
}

func main() {
	failed := false
	report := func(name string, ok bool) {
		failed = failed || !ok
		fmt.Println(map[bool]string{true: "OK  ", false: "FAIL "}[ok] + " " + name)
	}
	// 1. Six-step table (NOTES.md, gap=10), state after each event.
	w, _ := api.New(10, 0)
	want := "[100,100]x1 | [100,105]x2 | [100,105]x2;[130,130]x1 | [100,105]x2;[130,135]x2 | [100,105]x2;[118,118]x1;[130,135]x2 | [100,105]x3;[118,118]x1;[130,135]x2"
	steps, ok := []string{}, true
	for _, ts := range []int64{100, 105, 130, 135, 118, 100} {
		ok = feed(w, ts) == nil && ok
		steps = append(steps, fmtS(w.Snapshot("k")))
	}
	got := strings.Join(steps, " | ")
	report("six-step: "+got, ok && got == want)
	w2, _ := api.New(15, 0)
	_ = feed(w2, 100, 105, 130, 135, 118)
	report("118 bridges two sessions -> [100,135]x5",
		reflect.DeepEqual(w2.Snapshot("k"), []sess.Session{s1(100, 135, 5)}))
	w3, _ := api.New(10, 0)
	_ = feed(w3, 100, 100)
	report("duplicate TS bumps count only: [100,100]x2",
		reflect.DeepEqual(w3.Snapshot("k"), []sess.Session{s1(100, 100, 2)}))
	// 4. The same multiset under several shuffles gives identical results.
	ms := []int64{100, 105, 130, 135, 118, 100, 1, 200, 111, 134, 3, 2}
	rng := rand.New(rand.NewSource(7))
	var ref []sess.Session
	ok = true
	for round := 0; round < 5; round++ {
		ww, _ := api.New(10, 0)
		evs := make([]evt.Event, len(ms))
		for i, j := range rng.Perm(len(ms)) {
			evs[i] = evt.Event{Key: "k", TS: ms[j]}
		}
		ok = ww.Feed(evs) == nil && ok
		if g := ww.Snapshot("k"); round == 0 {
			ref = g
		} else if !reflect.DeepEqual(g, ref) {
			ok = false
		}
	}
	report("5 shuffled feeds identical", ok)
	report("matches sorted recomputation: "+fmtS(ref),
		reflect.DeepEqual(ref, oracle(append([]int64(nil), ms...), 10)))
	// 6. Three distinct, decidable sentinel errors.
	_, eGap := api.New(-3, 0)
	wc, _ := api.New(10, 1)
	_ = feed(wc, 0)
	eCap := feed(wc, 100)
	eKey := wc.Feed([]evt.Event{{Key: "", TS: 1}})
	report("three distinct sentinel errors", errors.Is(eGap, api.ErrInvalidGap) &&
		errors.Is(eCap, api.ErrTooManySessions) && errors.Is(eKey, api.ErrInvalidEvent) &&
		eGap.Error() != eCap.Error() && eCap.Error() != eKey.Error())
	// 7. Rejections leave no trace; the window stays usable afterwards.
	before := wc.Snapshot("k")
	_ = wc.Feed([]evt.Event{{Key: "new", TS: 9}, {Key: "", TS: 9}})
	_ = feed(wc, 50)
	trace := reflect.DeepEqual(wc.Snapshot("k"), before) && len(wc.Snapshot("new")) == 0
	_ = feed(wc, 5)
	report("rejected feeds leave no trace, still usable",
		trace && reflect.DeepEqual(wc.Snapshot("k"), []sess.Session{s1(0, 5, 2)}))
	// 8. Constant-touch correctness as m grows; probe bound pinned in sess.
	ok = true
	for _, m := range []int{100, 1000, 10000} {
		s := sess.NewSet(10, m+5)
		for j := 0; j < m; j++ {
			_ = s.Add(evt.Event{Key: "k", TS: int64(20 * j)})
		}
		ok = (s.Add(evt.Event{Key: "k", TS: 1}) == nil &&
			reflect.DeepEqual(s.Sessions("k")[0], s1(0, 1, 2))) && ok
	}
	report("constant-touch m=100..10000 (bound: sess.TestCompareBudget)", ok)
	// 9. Concurrent read-only access: all goroutines agree field by field.
	wr, _ := api.New(10, 0)
	_ = feed(wr, 100, 130, 105)
	base := wr.Snapshot("k")
	var bad atomic.Bool
	var wg sync.WaitGroup
	for g := 0; g < 24; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for it := 0; it < 100; it++ {
				if (id%3 == 0 && wr.SelfCheck() != nil) ||
					!reflect.DeepEqual(wr.Snapshot("k"), base) {
					bad.Store(true)
				}
			}
		}(g)
	}
	wg.Wait()
	report("24 concurrent readers agree field by field", !bad.Load())
	report("SelfCheck", w.SelfCheck() == nil && wr.SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}

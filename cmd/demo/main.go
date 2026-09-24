// Command demo exercises the CEP matcher: no args, no network, <=10 OK/FAIL lines.
package main

import "errors"
import "fmt"
import "math/rand"
import "os"
import "reflect"
import "sync"
import "sync/atomic"
import "ontology/api"
import "ontology/cepmatch"
import "ontology/cepwin"

var failed bool

func ck(name string, ok bool) {
	s := "OK"
	if !ok {
		s, failed = "FAIL", true
	}
	fmt.Println(s, name)
}

// naive is an independent O(n) reference for cross-checking the public matcher.
func naive(mode api.Mode, T int64, mp int, evs []api.Event) []api.Match {
	last, pend := map[string]api.Event{}, map[string][]api.Event{}
	var out []api.Match
	for _, ev := range evs {
		if mode == api.Strict {
			if p, ok := last[ev.Key]; ok && ev.Type == "B" && p.Type == "A" &&
				cepwin.InWindow(p.TS, ev.TS, T) {
				out = append(out, api.Match{A: p, B: ev})
			}
			last[ev.Key] = ev
			continue
		}
		alive := pend[ev.Key][:0]
		for _, a := range pend[ev.Key] {
			if !cepwin.Expired(a.TS, ev.TS, T) {
				alive = append(alive, a)
			}
		}
		if ev.Type == "A" && len(alive) < mp {
			alive = append(alive, ev)
		} else if ev.Type == "B" && len(alive) > 0 {
			out, alive = append(out, api.Match{A: alive[0], B: ev}), alive[1:]
		}
		pend[ev.Key], last[ev.Key] = alive, ev
	}
	return out
}

func gen(rng *rand.Rand, n int) []api.Event {
	ts, out := map[string]int64{}, make([]api.Event, n)
	for i := range out { // strictly increasing per key, so events are unique
		k := []string{"k", "z", "q"}[rng.Intn(3)]
		ts[k] += int64(1 + rng.Intn(3))
		out[i] = api.Event{Key: k, TS: ts[k], Type: []string{"A", "B", "X"}[rng.Intn(3)]}
	}
	return out
}

func ts(ms []api.Match) (r [][2]int64) {
	for _, m := range ms {
		r = append(r, [2]int64{m.A.TS, m.B.TS})
	}
	return
}

func main() {
	ten := []api.Event{
		{Key: "k", Type: "A", TS: 1}, {Key: "k", Type: "C", TS: 2},
		{Key: "k", Type: "B", TS: 3}, {Key: "k", Type: "A", TS: 4},
		{Key: "k", Type: "A", TS: 6}, {Key: "k", Type: "B", TS: 9},
		{Key: "k", Type: "B", TS: 11}, {Key: "k", Type: "A", TS: 12},
		{Key: "z", Type: "A", TS: 14}, {Key: "k", Type: "B", TS: 17},
	}
	feedTen := func(mode api.Mode) []api.Match {
		m, _ := api.New(mode, 5, 8)
		ms, _ := m.Feed(ten)
		return ms
	}
	ck("ten relaxed (1,3)(4,9)(6,11)(12,17)", reflect.DeepEqual(ts(feedTen(api.Relaxed)),
		[][2]int64{{1, 3}, {4, 9}, {6, 11}, {12, 17}}))
	ck("ten strict (6,9)(12,17)",
		reflect.DeepEqual(ts(feedTen(api.Strict)), [][2]int64{{6, 9}, {12, 17}}))

	bm, _ := api.New(api.Relaxed, 5, 8)
	eq, _ := bm.Feed([]api.Event{{Key: "k", Type: "A"}, {Key: "k", Type: "B", TS: 5}})
	ov, _ := bm.Feed([]api.Event{{Key: "k", Type: "A", TS: 6}, {Key: "k", Type: "B", TS: 12}})
	ck("window boundary delta==T matches, T+1 does not", len(eq) == 1 && len(ov) == 0)

	sm, _ := api.New(api.Strict, 5, 8)
	adj, _ := sm.Feed([]api.Event{{Key: "k", Type: "A", TS: 1}, {Key: "z", Type: "B", TS: 2},
		{Key: "z", Type: "A", TS: 3}, {Key: "k", Type: "B", TS: 6}})
	ck("strict: other-key events do not break adjacency", len(adj) == 1)

	okRand := true
	for seed := int64(0); seed < 20; seed++ {
		evs := gen(rand.New(rand.NewSource(seed)), 80)
		for _, mode := range []api.Mode{api.Strict, api.Relaxed} {
			m, _ := api.New(mode, 5, 1000)
			got, _ := m.Feed(evs)
			okRand = okRand && reflect.DeepEqual(ts(got), ts(naive(mode, 5, 1000, evs)))
		}
	}
	ck("random sequences agree with naive reference", okRand)

	_, e1 := api.New(api.Relaxed, -1, 8)
	em, _ := api.New(api.Relaxed, 5, 2)
	_, _ = em.Feed([]api.Event{{Key: "k", Type: "A", TS: 1}})
	before := em.Matches()
	_, e2 := em.Feed([]api.Event{{Key: "k", Type: "", TS: 2}})
	_, e3 := em.Feed([]api.Event{{Key: "k", Type: "A", TS: 0}})
	_, e4 := em.Feed([]api.Event{{Key: "k", Type: "A", TS: 2}, {Key: "k", Type: "A", TS: 3}})
	ck("four distinct decidable errors", errors.Is(e1, cepwin.ErrNegativeT) &&
		errors.Is(e2, cepwin.ErrEmptyField) && errors.Is(e3, cepmatch.ErrTimeRegression) &&
		errors.Is(e4, cepmatch.ErrPendingOverflow))
	unchanged := reflect.DeepEqual(em.Matches(), before) // rejects must leave zero trace
	after, _ := em.Feed([]api.Event{{Key: "k", Type: "B", TS: 6}})
	ck("rejected batch left no state; matcher still works",
		unchanged && len(after) == 1 && after[0].A.TS == 1)

	ck("large m probe stays constant (head-only processing)",
		cepmatch.ProbeHeadOnly(100) && cepmatch.ProbeHeadOnly(10000))

	fm, _ := api.New(api.Relaxed, 5, 100000)
	_, _ = fm.Feed(gen(rand.New(rand.NewSource(99)), 300))
	want := fm.Matches()
	start, bad := make(chan struct{}), atomic.Bool{}
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if !reflect.DeepEqual(fm.Matches(), want) {
				bad.Store(true)
			}
		}()
	}
	close(start)
	wg.Wait()
	ck("concurrent readers see identical match lists", !bad.Load())

	if failed {
		os.Exit(1)
	}
}

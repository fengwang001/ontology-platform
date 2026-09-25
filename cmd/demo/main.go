// Command demo prints OK/FAIL verdicts for the required checks.
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"sync"

	"ontology/api"
	"ontology/mbatch"
	"ontology/win"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Println(map[bool]string{true: "OK  ", false: "FAIL "}[ok] + name)
}

// closeOnce reports whether every (key,window) op sequence matches +(-+)*.
func closeOnce(log []mbatch.Change) bool {
	type pair struct {
		k string
		w win.Window
	}
	last := map[pair]byte{}
	for _, c := range log {
		p := pair{c.Key, c.Win}
		if c.Op == '+' && last[p] != 0 && last[p] != '-' {
			return false
		}
		if c.Op == '-' && last[p] != '+' {
			return false
		}
		last[p] = c.Op
	}
	return true
}

func main() {
	mk := func(op byte, s, e, n int64) mbatch.Change {
		return mbatch.Change{Op: op, Key: "K", Win: win.Window{Start: s, End: e}, Count: n}
	}
	eng := mbatch.New(10, 3)
	feed := func(tss ...int64) []mbatch.Change {
		var evs []mbatch.Event
		for _, ts := range tss {
			evs = append(evs, mbatch.Event{Key: "K", TS: ts})
		}
		return eng.Feed(evs)
	}
	check("batch1 +emissions, TS=20 boundary -> [20,30)",
		slices.Equal(feed(5, 12, 20), []mbatch.Change{mk('+', 0, 10, 1), mk('+', 10, 20, 1)}))
	check("batch2 late delta -(old=1)/+(2)",
		slices.Equal(feed(7, 22, 25), []mbatch.Change{mk('-', 0, 10, 1), mk('+', 0, 10, 2)}))
	check("batch3 close + & late old=2",
		slices.Equal(feed(8, 31, 15), []mbatch.Change{mk('+', 20, 30, 3), mk('-', 0, 10, 2), mk('+', 0, 10, 3), mk('-', 10, 20, 1), mk('+', 10, 20, 2)}))
	feed(9) // tail batch: no trigger
	check("flush tail + & close-once deltas",
		slices.Equal(eng.Flush(), []mbatch.Change{mk('+', 30, 40, 1), mk('-', 0, 10, 3), mk('+', 0, 10, 4)}) &&
			closeOnce(eng.Emitted()))
	check("close-scan reads independent of m", mbatch.VerifyCloseScan() == nil)

	aeng, _ := api.New(10, 3)
	var evs []api.Event
	for _, ts := range []int64{5, 12, 20, 7, 22, 25, 8, 31, 15, 9} {
		evs = append(evs, api.Event{Key: "K", TS: ts})
	}
	if _, err := aeng.Feed(evs); err != nil {
		check("api feed", false)
	}
	aeng.Flush()
	want := map[api.Window]int64{}
	for _, ev := range evs {
		want[win.Of(win.Index(ev.TS, 10), 10)]++
	}
	check("view == batch recompute",
		reflect.DeepEqual(aeng.View(), map[string]map[api.Window]int64{"K": want}))
	type pair struct {
		k string
		w api.Window
	}
	seen := map[pair]int64{}
	prefixOK := true
	for _, c := range aeng.Emitted() {
		p := pair{c.Key, c.Win}
		if c.Op == '-' {
			if n, ok := seen[p]; !ok || n != c.Count {
				prefixOK = false
			}
			delete(seen, p)
		} else {
			seen[p] = c.Count
		}
	}
	check("changelog prefixes self-consistent", prefixOK)
	_, e1 := api.New(0, 1)
	_, e2 := api.New(1, 0)
	bad, _ := api.New(10, 3)
	_, e3 := bad.Feed([]api.Event{{Key: "", TS: 1}})
	check("three decidable errors, mutually distinct",
		errors.Is(e1, api.ErrNonPositiveWindow) && errors.Is(e2, api.ErrNonPositiveBatch) &&
			errors.Is(e3, api.ErrEmptyKey) &&
			!errors.Is(e1, e2) && !errors.Is(e2, e3) && !errors.Is(e1, e3))
	nt, _ := api.New(10, 3)
	nt.Feed([]api.Event{{Key: "K", TS: 5}})                               // buffer 1/3
	nt.Feed([]api.Event{{Key: "K", TS: 6}, {Key: "", TS: 7}})             // rejected wholesale
	got, _ := nt.Feed([]api.Event{{Key: "K", TS: 8}, {Key: "K", TS: 12}}) // buffer 3/3 -> trigger
	check("rejected feed leaves no trace", len(got) > 0 && len(nt.Emitted()) == len(got))

	const g = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	views := make([]map[string]map[api.Window]int64, g)
	for i := 0; i < g; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			for j := 0; j < 50; j++ {
				views[i] = aeng.View()
				_ = aeng.Emitted()
				_ = aeng.SelfCheck()
			}
		}(i)
	}
	close(start)
	wg.Wait()
	same := true
	for _, v := range views {
		same = same && reflect.DeepEqual(v, views[0])
	}
	check("concurrent read-only views identical", same)

	if failed {
		os.Exit(1)
	}
}

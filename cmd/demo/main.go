package main

import (
	"errors"
	"fmt"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/cagg"
	"ontology/cwin"
)

type out = cagg.Out

func main() {
	fails := 0
	check := func(name string, cond bool) {
		if cond {
			fmt.Println("OK", name)
		} else {
			fails++
			fmt.Println("FAIL", name)
		}
	}

	check("cwin floor start/Emin",
		cwin.Start(-5, 12) == -12 && cwin.Start(12, 12) == 12 &&
			cwin.MinSub(-5, -12, 4) == 2 && cwin.End(-12, 4, 2) == -4)

	tss := []int64{-5, 1, -2, 6, 3, 11, 12, 19}
	want := [][]out{
		{{Key: "K", Start: -12, End: -8, Count: 0}},
		{{Key: "K", Start: -12, End: -4, Count: 1}},
		nil,
		{{Key: "K", Start: -12, End: 0, Count: 2}, {Key: "K", Start: 0, End: 4, Count: 1}},
		nil,
		{{Key: "K", Start: 0, End: 8, Count: 2}},
		nil,
		{{Key: "K", Start: 0, End: 12, Count: 3}, {Key: "K", Start: 12, End: 16, Count: 1}},
	}
	w, _ := api.New(12, 4, 2, 2)
	walkOK := true
	for i, ts := range tss {
		got, err := w.Feed([]cagg.Event{{Key: "K", TS: ts}})
		if err != nil || !reflect.DeepEqual(got, want[i]) {
			walkOK = false
		}
	}
	flushed := w.Flush()
	walkOK = walkOK && reflect.DeepEqual(flushed,
		[]out{{Key: "K", Start: 12, End: 20, Count: 2}, {Key: "K", Start: 12, End: 24, Count: 2}})
	check("8 events + flush outputs", walkOK)
	check("step5 dropped: wm==Emin", w.Dropped() == 1)

	var batch []out
	for _, step := range want {
		batch = append(batch, step...)
	}
	check("flush All()==batch recompute", reflect.DeepEqual(w.All(), append(batch, flushed...)))
	check("cumulative monotone", monotone(w.All()))

	_, e1 := cagg.New(12, 5, 2, 2)
	b, _ := cagg.New(12, 4, 2, 1)
	b.Feed([]cagg.Event{{Key: "K", TS: -5}})
	before, drop := b.All(), b.Dropped()
	_, e2 := b.Feed([]cagg.Event{{Key: "K", TS: 1}})
	_, e3 := b.Feed([]cagg.Event{{Key: "", TS: 1}})
	noTrace := reflect.DeepEqual(b.All(), before) && b.Dropped() == drop
	_, reusable := b.Flush(), b.All() // rejected batch left it usable
	check("3 distinct sentinel errors",
		errors.Is(e1, cagg.ErrInvalidParams) && errors.Is(e2, cagg.ErrTooManyWindows) &&
			errors.Is(e3, cagg.ErrEmptyKey))
	check("rejected op leaves no trace, reusable", noTrace && len(reusable) == 3)
	check("scan count constant in m", cagg.VerifyScanBound() == nil)

	par, _ := api.New(12, 4, 2, 100)
	par.Feed([]cagg.Event{{Key: "K", TS: -5}, {Key: "K", TS: 1}, {Key: "Q", TS: 20}})
	par.Flush()
	ref := par.All()
	var wg sync.WaitGroup
	same := true
	var mu sync.Mutex
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g := par.All()
			mu.Lock()
			if !reflect.DeepEqual(g, ref) || par.Dropped() != 0 {
				same = false
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	check("concurrent reads identical", same)
	check("SelfCheck four invariants", func() bool { e := w.SelfCheck(); return e == nil }())

	if fails > 0 {
		panic("demo checks failed")
	}
}

func monotone(all []out) bool {
	prev := map[[2]any]int64{}
	for _, o := range all {
		k := [2]any{o.Key, o.Start}
		if p, ok := prev[k]; ok && o.Count < p {
			return false
		}
		prev[k] = o.Count
	}
	return true
}

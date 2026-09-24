package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/cntwin"
	"ontology/cwagg"
)

var fails int

func ok(cond bool, name string) {
	s := "OK   "
	if !cond {
		s = "FAIL "
		fails++
	}
	fmt.Println(s + name)
}

func ev(k string, p, v int64) cwagg.Event { return cwagg.Event{Key: k, Pos: p, Val: v} }

func main() {
	// cntwin: window assignment / trigger / lateness arithmetic,
	// including the (乙)(丙) boundary values from NOTES.md.
	ok(cntwin.Window(5, 5) == 1 && cntwin.Window(4, 5) == 0 &&
		cntwin.Triggered(5, 5) && !cntwin.Triggered(4, 5) &&
		cntwin.Late(7, 9) && cntwin.Acceptable(7, 9, 2) && !cntwin.Acceptable(6, 9, 2),
		"cntwin: win(5,5)=1, trigger at cnt==size, late accept 7>=9-2 inclusive")

	// cwagg: the section-3 sequence (size=5, lateness=2), step by step.
	a := cwagg.New(5, 2)
	evs := []cwagg.Event{ev("k", 0, 10), ev("k", 1, 20), ev("k", 2, 30), ev("k", 3, 40), ev("k", 4, 50)}
	calm := true
	for _, e := range evs[:4] { // steps 1-4: wm=0..3, win0 cnt=1..4, no fire, no drop
		a.Add(e)
		calm = calm && len(a.Fired()) == 0 && a.Dropped() == 0
	}
	ok(calm, "cwagg steps1-4: win0 cnt=1..4, wm=0..3, all normal, no fire")
	a.Add(evs[4]) // step 5: (4,50) closes window [0,5)
	f := a.Fired()
	ok(len(f) == 1 && f[0] == (cwagg.Fire{Key: "k", Win: 0, Sum: 150}),
		"cwagg step5: (4,50) fires win[0,5) with sum=150")
	a.Add(ev("k", 5, 60)) // step 6: -> window 1 (misassignment to closed win0 would drop it)
	a.Add(ev("k", 9, 90)) // step 7: wm=9
	a.Add(ev("k", 7, 70)) // step 8: late, 7>=9-2 -> accepted (a drop would show in Dropped)
	w := cwagg.New(5, 2)  // fresh key: window [5,10) fills and fires
	for _, e := range []cwagg.Event{ev("w", 5, 60), ev("w", 6, 1), ev("w", 7, 70), ev("w", 8, 1), ev("w", 9, 90)} {
		w.Add(e)
	}
	f = w.Fired()
	ok(len(a.Fired()) == 1 && a.Dropped() == 0 && len(f) == 1 && f[0] == (cwagg.Fire{Key: "w", Win: 1, Sum: 222}),
		"cwagg steps6-8: (5,60)->win1, wm=9, (7,70) late-accepted; win[5,10) fires 222")

	// api: constructor validation and four distinct sentinel errors.
	_, e1 := api.New(0, 0)
	_, e2 := api.New(1, -1)
	sent := map[error]bool{api.ErrSize: true, api.ErrLateness: true, api.ErrPos: true, api.ErrKey: true}
	ok(errors.Is(e1, api.ErrSize) && errors.Is(e2, api.ErrLateness) && len(sent) == 4,
		"api: New rejects size<=0 / lateness<0; four distinct sentinels")

	// api: rejected feeds (bad Pos, empty Key, one bad poisons the batch) leave no trace.
	g, _ := api.New(2, 0)
	_, _ = g.Feed([]api.Event{ev("k", 0, 5)})
	_, errP := g.Feed([]api.Event{ev("k", 1, 1), ev("k", -2, 1)})
	_, errK := g.Feed([]api.Event{ev("", 1, 1)})
	clean := errors.Is(errP, api.ErrPos) && errors.Is(errK, api.ErrKey) &&
		len(g.Fired()) == 0 && g.Dropped() == 0
	fs, _ := g.Feed([]api.Event{ev("k", 1, 7)}) // still usable afterwards
	ok(clean && len(fs) == 1 && fs[0] == (api.Fire{Key: "k", Win: 0, Sum: 12}),
		"api: bad Pos/Key rejected with no trace; instance still usable")

	// api: SelfCheck verifies all four invariants on built-in sequences.
	ok(g.SelfCheck() == nil, "api: SelfCheck (batch-recompute, fire-once, wm/cnt, no-trace)")

	// api: Fired equals an independent batch recomputation; each window fires once.
	g3, _ := api.New(7, 0)
	var gen []api.Event
	for p := int64(0); p < 40; p++ {
		for _, k := range []string{"a", "b", "c"} {
			gen = append(gen, ev(k, p, p+int64(len(k))))
		}
	}
	_, _ = g3.Feed(gen)
	type kw struct {
		k string
		w int64
	}
	sum, cnt, got := map[kw]int64{}, map[kw]int64{}, map[kw]int64{}
	for _, e := range gen {
		id := kw{e.Key, e.Pos / 7}
		sum[id] += e.Val
		cnt[id]++
	}
	dup := false
	for _, f := range g3.Fired() {
		id := kw{f.Key, f.Win}
		if _, seen := got[id]; seen {
			dup = true
		}
		got[id] = f.Sum
	}
	full, match := 0, true
	for id, c := range cnt {
		if c == 7 {
			full++
			match = match && got[id] == sum[id]
		}
	}
	ok(match && !dup && full == len(got), "api: Fired == batch recompute; each window fires once")

	// cwagg: trigger check inspects O(1) windows regardless of open-window count
	// (white-box assertion lives in cwagg's internal test; the counter is
	// unexported, so the demo delegates to `go test` instead of reading it).
	goBin, err := exec.LookPath("go")
	if err != nil {
		goBin = "/usr/local/go/bin/go"
	}
	err = exec.Command(goBin, "test", "-run", "TestCheckedWindowsConstant", "./cwagg/").Run()
	ok(err == nil, "cwagg: trigger check is O(1) in open windows (m=100..10000)")

	// api: concurrent readers observe identical state (run with -race in tests).
	wantF, wantD := g3.Fired(), g3.Dropped()
	var wg sync.WaitGroup
	var bad atomic.Int32
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !slices.Equal(g3.Fired(), wantF) || g3.Dropped() != wantD || g3.SelfCheck() != nil {
				bad.Add(1)
			}
		}()
	}
	wg.Wait()
	ok(bad.Load() == 0, "api: concurrent Fired/Dropped/SelfCheck identical")

	if fails > 0 {
		os.Exit(1)
	}
}

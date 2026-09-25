package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/wtm"
)

var failed bool

func ok(b bool) string {
	if b {
		return "OK"
	}
	failed = true
	return "FAIL"
}

func check(name string, b bool) { fmt.Printf("%s %s\n", name, ok(b)) }

// step is one of the eight NOTES operations: feed==true is Feed(key,
// ts, pt), feed==false is Tick(pt).
type step struct {
	feed     bool
	key      string
	ts, pt   int64
	wantWM   int64
	wantDrop int
}

func main() {
	// wtm: watermark advances by max, idle uses processing time, late
	// is strict (equal is accepted).
	check("wtm", wtm.Advance(17, 7) == 17 &&
		wtm.Idle(10, 1, 5) && !wtm.Idle(12, 11, 5) &&
		!wtm.Late(17, 17) && wtm.Late(22, 27))

	// steps: after each of the eight NOTES operations print wm/dropped;
	// final holds the end state wm=37 dropped=1.
	steps := []step{
		{true, "a", 10, 0, 7, 0},
		{true, "b", 20, 1, 17, 0},
		{false, "", 0, 10, 17, 0},
		{true, "c", 20, 11, 17, 0},
		{false, "", 0, 12, 17, 0},
		{false, "", 0, 30, 27, 0},
		{true, "d", 25, 31, 27, 1},
		{false, "", 0, 40, 37, 1},
	}
	f, _ := api.New(3, 5)
	stepsOK, seq := true, ""
	for _, st := range steps {
		if st.feed {
			_ = f.Feed(st.key, st.ts, st.pt)
		} else {
			_ = f.Tick(st.pt)
		}
		seq += fmt.Sprintf("%d/%d ", f.Watermark(), f.Dropped())
		if f.Watermark() != st.wantWM || f.Dropped() != st.wantDrop {
			stepsOK = false
		}
	}
	fmt.Printf("steps %s %s\n", seq, ok(stepsOK))
	check("final", f.Watermark() == 37 && f.Dropped() == 1)

	// errors: three distinct decidable sentinels.
	_, perr := api.New(0, 5)
	check("errors", errors.Is(perr, api.ErrInvalidParams) &&
		errors.Is(f.Feed("", 1, 0), api.ErrEmptyKey) &&
		errors.Is(f.Feed("x", -1, 0), api.ErrNegativeValue) &&
		errors.Is(f.Tick(-1), api.ErrNegativeValue))

	// notrace: rejected calls change neither watermark nor dropped.
	w0, d0 := f.Watermark(), f.Dropped()
	_ = f.Feed("", 1, 0)
	_ = f.Feed("x", 1, -1)
	_ = f.Tick(-9)
	check("notrace", f.Watermark() == w0 && f.Dropped() == d0)

	// constm: after m events one late event is dropped identically at
	// every m; the decision never rescans history (examined count ==0
	// is asserted in the idle white-box test; counter is not exported).
	constOK := true
	for _, m := range []int{100, 1000, 10000} {
		g, _ := api.New(3, 5)
		for i := 0; i < m; i++ {
			_ = g.Feed(fmt.Sprintf("k%d", i), int64(100+i), int64(i))
		}
		hi := g.Watermark()
		_ = g.Feed("late", 0, int64(m))
		if g.Dropped() != 1 || g.Watermark() != hi {
			constOK = false
		}
	}
	check("constm", constOK)

	// concur: N concurrent readers see one identical watermark;
	// concurrent advancers each observe a non-decreasing watermark.
	const n = 16
	cf, _ := api.New(3, 5)
	_ = cf.Feed("a", 10, 0)
	base := cf.Watermark()
	var wg sync.WaitGroup
	got := make([]int64, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); got[i] = cf.Watermark() }(i)
	}
	wg.Wait()
	readSame := true
	for _, v := range got {
		if v != base {
			readSame = false
		}
	}
	mono := true
	var mu sync.Mutex
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			prev := cf.Watermark()
			for j := 0; j < 20; j++ {
				_ = cf.Tick(int64((i+1)*(j+1)*50 + 1000))
				if v := cf.Watermark(); v < prev {
					mu.Lock()
					mono = false
					mu.Unlock()
				} else {
					prev = v
				}
			}
		}(i)
	}
	wg.Wait()
	check("concur", readSame && mono)

	// selfcheck: all four invariants on a private built-in sequence.
	check("selfcheck", f.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}

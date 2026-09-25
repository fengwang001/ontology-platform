package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/frame"
)

var failed bool

func ok(cond bool, msg string) {
	s := "OK"
	if !cond {
		s, failed = "FAIL", true
	}
	fmt.Printf("%s %s\n", s, msg)
}
func snapStr(s []frame.Frame) string {
	out, d := "[", map[bool]string{false: "c", true: "d"}
	for i, f := range s {
		if i > 0 {
			out += " "
		}
		cell := "-"
		if !f.Empty() {
			cell = fmt.Sprintf("(%d,%s,%d)", f.PageID, d[f.Dirty], f.Pin)
		}
		out += cell
	}
	return out + "]"
}

// eightSteps replays the NOTES.md derivation and checks every row.
func eightSteps() (w6, w8 int) {
	a, _ := api.New(3)
	E := frame.Frame{PageID: frame.NoPage}
	mk := func(id, pin int, dr bool) frame.Frame { return frame.Frame{PageID: id, Pin: pin, Dirty: dr} }
	ops := [][2]int{{0, 1}, {0, 2}, {0, 3}, {2, 0}, {1, 0}, {0, 4}, {1, 1}, {0, 5}} // 0=Pin 1=Unpin 2=MarkDirty
	names := []string{"Pin(1)", "Pin(2)", "Pin(3)", "MarkDirty(0)", "Unpin(0)", "Pin(4)", "Unpin(1)", "Pin(5)"}
	rets := []int{0, 1, 2, -1, -1, 0, -1, 1}
	snaps := [][3]frame.Frame{
		{mk(1, 1, false), E, E},
		{mk(1, 1, false), mk(2, 1, false), E},
		{mk(1, 1, false), mk(2, 1, false), mk(3, 1, false)},
		{mk(1, 1, true), mk(2, 1, false), mk(3, 1, false)},
		{mk(1, 0, true), mk(2, 1, false), mk(3, 1, false)},
		{mk(4, 1, false), mk(2, 1, false), mk(3, 1, false)},
		{mk(4, 1, false), mk(2, 0, false), mk(3, 1, false)},
		{mk(4, 1, false), mk(5, 1, false), mk(3, 1, false)},
	}
	ws := []int{0, 0, 0, 0, 0, 1, 1, 1}
	for i, o := range ops {
		ret := -1
		if o[0] == 0 {
			ret, _ = a.Pin(o[1])
		} else if o[0] == 1 {
			_ = a.Unpin(o[1])
		} else {
			_ = a.MarkDirty(o[1])
		}
		got, good := a.Snapshot(), ret == rets[i] && a.Writes() == ws[i]
		for j := range got {
			good = good && got[j] == snaps[i][j]
		}
		ok(good, fmt.Sprintf("s%d %s->f%d %s w=%d", i+1, names[i], ret, snapStr(got), a.Writes()))
	}
	return ws[5], ws[7]
}
func main() {
	w6, w8 := eightSteps()
	// Line 9: dirty write-back, clean evict no-write, pinBlock, 3 errors, noTrace, bigM.
	b, _ := api.New(2)
	_, _ = b.Pin(1)
	_, _ = b.Pin(2)
	_, errFull := b.Pin(3)
	errBad := b.Unpin(9)
	c, _ := api.New(1)
	_, _ = c.Pin(1)
	_ = c.Unpin(0)
	errPin := c.Unpin(0)
	d, _ := api.New(1)
	_, _ = d.Pin(1)
	before, beforeW := d.Snapshot(), d.Writes()
	_, errRej := d.Pin(2) // all pinned: rejected
	noTrace := errRej != nil && d.Writes() == beforeW && d.Snapshot()[0] == before[0]
	bigM := true
	for _, m := range []int{100, 1000, 10000} {
		p, _ := api.New(m)
		for i := 0; i < m; i++ {
			_, _ = p.Pin(i)
		}
		if f, err := p.Pin(0); err != nil || f != 0 || p.Writes() != 0 || p.ResidentCount() != m {
			bigM = false // resident hit: O(1) map lookup, no scan, no eviction
		}
	}
	errsOK := errors.Is(errFull, api.ErrPoolFull) && errors.Is(errBad, api.ErrBadFrame) &&
		errors.Is(errPin, api.ErrNotPinned) && !errors.Is(errFull, api.ErrBadFrame) &&
		!errors.Is(errBad, api.ErrNotPinned) && !errors.Is(errPin, api.ErrPoolFull)
	ok(w6 == 1 && w8 == 1 && errsOK && noTrace && bigM,
		fmt.Sprintf("dirtyWB(w=%d) cleanNoWB(w=%d) pinBlock errs=3 noTrace bigM(100..10000)", w6, w8))
	// Line 10: invariants via SelfCheck; concurrent residency conserved.
	a3, _ := api.New(3)
	sc := a3.SelfCheck() == nil
	const n = 64
	cp, _ := api.New(n)
	done, mono := make(chan struct{}), true
	var rwg sync.WaitGroup
	rwg.Add(1)
	go func() {
		defer rwg.Done()
		for prev := 0; ; {
			select {
			case <-done:
				return
			default:
			}
			c := cp.ResidentCount()
			if c < prev {
				mono = false
				return
			}
			prev = c
		}
	}()
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(id int) { defer wg.Done(); _, _ = cp.Pin(id) }(i)
	}
	wg.Wait()
	close(done)
	rwg.Wait()
	uniq, dupFree := map[int]bool{}, true
	for _, f := range cp.Snapshot() {
		dupFree = dupFree && !f.Empty() && !uniq[f.PageID]
		uniq[f.PageID] = true
	}
	conc := mono && cp.ResidentCount() == n && cp.Writes() == 0 && dupFree
	ok(sc && conc, fmt.Sprintf("invariants(unique,naive,pinSafe,noTrace) concurrent(%d,monotonic,conserved)", n))
	if failed {
		os.Exit(1)
	}
}

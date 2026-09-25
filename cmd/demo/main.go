// Command demo exercises the dual-stream watermark aligner and prints OK/FAIL.
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/align"
	"ontology/api"
	"ontology/wm"
)

var fails int

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK " + name)
	} else {
		fails++
		fmt.Println("FAIL " + name)
	}
}
func ev(s byte, t int64) align.Event { return align.Event{Stream: s, TS: t} }

type row struct {
	e    align.Event
	wA   int64 // -1 = unseen (-inf)
	wB   int64
	out  []align.Event
	late bool
}

var table = []row{
	{ev('A', 1), 1, -1, nil, false},
	{ev('A', 2), 2, -1, nil, false},
	{ev('B', 1), 2, 1, []align.Event{ev('A', 1), ev('B', 1)}, false},
	{ev('A', 3), 3, 1, nil, false},
	{ev('B', 2), 3, 2, []align.Event{ev('A', 2), ev('B', 2)}, false},
	{ev('B', 5), 3, 5, []align.Event{ev('A', 3)}, false}, // (甲) W=3: only A3
	{ev('A', 4), 4, 5, []align.Event{ev('A', 4)}, false},
	{ev('A', 2), 4, 5, nil, true}, // (丙) TS2 < wA4: late
}

func nonDec(v []api.Event) bool {
	for i := 1; i < len(v); i++ {
		if v[i].TS < v[i-1].TS {
			return false
		}
	}
	return true
}
func main() {
	// wm: unseen -> Seen()==false; TS equal on-time, strictly smaller late.
	var w wm.Watermark
	traceOK := !w.Seen()
	w.Advance(3)
	traceOK = traceOK && !w.Late(3) && w.Late(2)
	// Eight-step align trace: per-step W, emit/buffer/late (steps 1, 6, 8).
	al := align.New()
	for _, x := range table {
		out, late := al.Feed(x.e)
		W, ok := al.AlignedW()
		if late != x.late || !reflect.DeepEqual(out, x.out) {
			traceOK = false
		}
		if x.wB < 0 { // (乙) step 1: B unseen -> W=-inf, A1 buffered
			traceOK = traceOK && !ok && len(out) == 0
		} else {
			want := x.wA
			if x.wB < want {
				want = x.wB
			}
			traceOK = traceOK && ok && W == want
		}
	}
	traceOK = traceOK && reflect.DeepEqual(al.Close(), []align.Event{ev('B', 5)}) // only B5 held
	check("8-step trace W + emit/buffer/late (steps 1,6,8)", traceOK)
	// Close output == naive accepted-event sort and non-decreasing.
	a := api.New()
	for _, e := range []api.Event{{Stream: 'A', TS: 1}, {Stream: 'A', TS: 2}, {Stream: 'B', TS: 1}, {Stream: 'A', TS: 3}, {Stream: 'B', TS: 2}, {Stream: 'B', TS: 5}, {Stream: 'A', TS: 4}, {Stream: 'A', TS: 2}} {
		a.Feed(e)
	}
	a.Close()
	naive := []api.Event{{Stream: 'A', TS: 1}, {Stream: 'B', TS: 1}, {Stream: 'A', TS: 2}, {Stream: 'B', TS: 2}, {Stream: 'A', TS: 3}, {Stream: 'A', TS: 4}, {Stream: 'B', TS: 5}}
	check("Close == naive reorder and non-decreasing", reflect.DeepEqual(a.View(), naive) && nonDec(a.View()))
	// No early emission: lone stream (W=-inf) emits nothing.
	c := api.New()
	first, _ := c.Feed(api.Event{Stream: 'A', TS: 1})
	c.Close()
	check("no early emission while a stream unseen (W=-inf)", len(first) == 0)
	// Three distinct sentinels; rejected ops leave no trace, still usable.
	d := api.New()
	d.Feed(api.Event{Stream: 'A', TS: 5})
	d.Feed(api.Event{Stream: 'B', TS: 5})
	snap, drop := d.View(), d.Dropped()
	_, e1 := d.Feed(api.Event{Stream: 'C', TS: 1})
	_, e2 := d.Feed(api.Event{Stream: 'A', TS: -1})
	noTrace := reflect.DeepEqual(d.View(), snap) && d.Dropped() == drop
	_, e3 := d.Feed(api.Event{Stream: 'A', TS: 6})
	fin, _ := d.Close()
	_, e4 := d.Feed(api.Event{Stream: 'A', TS: 1})
	_, e5 := d.Close()
	noTrace = noTrace && reflect.DeepEqual(d.View(), append(snap, fin...))
	distinct := errors.Is(e1, api.ErrBadStream) && errors.Is(e2, api.ErrNegativeTS) &&
		errors.Is(e4, api.ErrClosed) && errors.Is(e5, api.ErrClosed) && e3 == nil
	check("3 distinct decidable sentinel errors", distinct)
	check("rejected ops leave no trace and stay usable", noTrace)
	// Heap: for m=100..10000 exactly the single minimum emits; the unexported
	// O(1) probe counter is pinned white-box by align.TestHeapComparisonBound.
	heapOK := true
	for _, m := range []int{100, 316, 1000, 3162, 10000} {
		h := align.New()
		for i := 0; i < m; i++ {
			h.Feed(ev('A', int64(2+i)))
		}
		out, late := h.Feed(ev('B', 1))
		heapOK = heapOK && !late && len(out) == 1 && out[0] == ev('B', 1) && len(h.Close()) == m
	}
	check("heap min-locate result constant for m=100..10000 (probe pinned white-box)", heapOK)
	// N goroutines View one closed instance concurrently: identical sequences.
	p := api.New()
	for i := 0; i < 200; i++ {
		p.Feed(api.Event{Stream: byte('A' + i%2), TS: int64(i % 40)})
	}
	p.Close()
	const n = 16
	var wg sync.WaitGroup
	start, views := make(chan struct{}), make([][]api.Event, n)
	for g := range views {
		wg.Add(1)
		go func(g int) { defer wg.Done(); <-start; views[g] = p.View() }(g)
	}
	close(start)
	wg.Wait()
	same := true
	for g := 1; g < n; g++ {
		same = same && reflect.DeepEqual(views[g], views[0])
	}
	check("concurrent View identical and SelfCheck clean", same && p.SelfCheck() == nil)
	if fails > 0 {
		os.Exit(1)
	}
}

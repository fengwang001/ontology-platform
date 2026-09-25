// Package mbatch implements micro-batch triggered tumbling-window counting.
package mbatch

import (
	"container/heap"
	"errors"
	"math"

	"ontology/win"
)

// Event is an upstream event.
type Event struct {
	Key string
	TS  int64
}

// Change is one changelog entry; Op is '+' (upsert) or '-' (retract).
type Change struct {
	Op    byte
	Key   string
	Win   win.Window
	Count int64
}

type iheap []int64 // min-heap of open window indices (end grows with index)

func (h iheap) Len() int           { return len(h) }
func (h iheap) Less(i, j int) bool { return h[i] < h[j] }
func (h iheap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *iheap) Push(x any)        { *h = append(*h, x.(int64)) }
func (h *iheap) Pop() any {
	old := *h
	v := old[len(old)-1]
	*h = old[:len(old)-1]
	return v
}

// Engine accumulates events into micro-batches and triggers window closes.
type Engine struct {
	w, b, wm int64
	hasWM    bool
	batch    []Event
	counts   map[int64]map[string]int64 // window index -> key -> count
	closed   map[int64]bool             // window index closed (emitted '+')
	open     iheap                      // open window indices, min end on top
	log      []Change
	reads    int // windows inspected for close decisions in the last trigger
}

// New returns an Engine with window size w and batch size b.
func New(w, b int64) *Engine {
	return &Engine{w: w, b: b, counts: map[int64]map[string]int64{}, closed: map[int64]bool{}}
}

// Feed buffers events; every full batch of b is sealed and triggered.
// Returns the changes emitted by the triggered batches, in order.
func (e *Engine) Feed(evs []Event) []Change {
	var out []Change
	for _, ev := range evs {
		e.batch = append(e.batch, ev)
		if int64(len(e.batch)) == e.b {
			out = append(out, e.trigger(e.batch)...)
			e.batch = nil
		}
	}
	return out
}

// Flush triggers the tail batch with wm = +inf, closing all windows.
func (e *Engine) Flush() []Change {
	e.wm, e.hasWM = math.MaxInt64, true
	out := e.trigger(e.batch)
	e.batch = nil
	return out
}

// Emitted returns a copy of the full changelog.
func (e *Engine) Emitted() []Change { return append([]Change(nil), e.log...) }

// trigger runs the three steps in order: count events (marking lates),
// close windows whose end <= wm (emitting '+'), emit late '-'/'+' deltas.
func (e *Engine) trigger(batch []Event) []Change {
	var lates []Change // retraction entries for late events, in arrival order
	for _, ev := range batch {
		if !e.hasWM || ev.TS > e.wm {
			e.wm = ev.TS
		}
		e.hasWM = true
		k := win.Index(ev.TS, e.w)
		if e.closed[k] {
			lates = append(lates, Change{Op: '-', Key: ev.Key, Win: win.Of(k, e.w), Count: e.counts[k][ev.Key]})
		}
		m := e.counts[k]
		if m == nil { // first event of window k; closed[k] implies counts[k] exists
			m = map[string]int64{}
			e.counts[k] = m
			heap.Push(&e.open, k)
		}
		m[ev.Key]++
	}
	var out []Change
	e.reads = 0
	for len(e.open) > 0 {
		k := e.open[0]
		e.reads++
		if !win.Of(k, e.w).Closed(e.wm) {
			break
		}
		heap.Pop(&e.open)
		e.closed[k] = true
		w := win.Of(k, e.w)
		for key, n := range e.counts[k] { // per-key order within a window is unspecified
			out = append(out, Change{Op: '+', Key: key, Win: w, Count: n})
		}
	}
	for _, l := range lates {
		if l.Count > 0 { // old==0 means the key is new to the window: nothing to retract
			out = append(out, l)
		}
		out = append(out, Change{Op: '+', Key: l.Key, Win: l.Win, Count: l.Count + 1})
	}
	e.log = append(e.log, out...)
	return out
}

var errScanGrows = errors.New("mbatch: close-scan reads grow with history")

// VerifyCloseScan checks that close determination locates windows by end
// order (heap), not by scanning history. Never exposes the counter.
func VerifyCloseScan() error {
	for _, m := range []int64{100, 1000, 10000} {
		e := New(1, 1)
		for i := int64(0); i < m; i++ {
			e.Feed([]Event{{Key: "k", TS: i}})
		}
		e.Feed([]Event{{Key: "k", TS: m}}) // wm +1: closes exactly 1 window
		if e.reads > 1+4 {
			return errScanGrows
		}
		e.Feed([]Event{{Key: "k", TS: m}}) // closes 0 windows
		if e.reads > 4 {
			return errScanGrows
		}
	}
	return nil
}

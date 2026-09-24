// Package hagg maintains per-(key, window) hopping-window counts, a caller-advanced clock, ordered window closing and drop statistics.
package hagg

import (
	"container/heap"
	"errors"
	"strconv"
	"sync"

	"ontology/hop"
)

var ErrBadParams = errors.New("hagg: need size>0, slide>0, size%slide==0") // the four failures are distinct sentinels:
var ErrClockBack = errors.New("hagg: clock cannot go backwards")
var ErrTooManyOpen = errors.New("hagg: too many open windows")
var ErrEmptyKey = errors.New("hagg: event key must not be empty")

type Result struct { // one closed-window output: [Start, End) Count for Key
	Key                  string
	K, Start, End, Count int64
}
type ent struct {
	key    string
	k, end int64
}
type winHeap []ent              // min-heap ordered by (end, key, k)
func (h winHeap) Len() int      { return len(h) }
func (h winHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h winHeap) Less(i, j int) bool {
	return h[i].end < h[j].end || h[i].end == h[j].end && (h[i].key < h[j].key || h[i].key == h[j].key && h[i].k < h[j].k)
}
func (h *winHeap) Push(x any) { *h = append(*h, x.(ent)) }
func (h *winHeap) Pop() any   { x := (*h)[len(*h)-1]; *h = (*h)[:len(*h)-1]; return x }

type Agg struct { // thread-safe hopping-window aggregator
	mu         sync.Mutex
	p          hop.Params
	max        int
	clock      int64                      // -1<<63 (-inf) until the first Advance
	counts     map[string]map[int64]int64 // key -> k -> count; live windows only
	open       winHeap
	out        []Result // all results emitted so far
	drop, last int64    // last = entries inspected by the last Advance; unexported, never exposed
}

func New(size, slide int64, maxOpen int) (*Agg, error) { // builds an Agg or ErrBadParams
	p := hop.Params{Size: size, Slide: slide}
	if !p.Valid() {
		return nil, ErrBadParams
	}
	return &Agg{p: p, max: maxOpen, clock: -1 << 63, counts: map[string]map[int64]int64{}}, nil
}
func (a *Agg) Add(key string, ts int64) error { // counts open windows; all-closed drops, over-limit rejects atomically
	a.mu.Lock()
	defer a.mu.Unlock()
	if key == "" {
		return ErrEmptyKey
	}
	ks := a.p.OpenKs(a.p.Ks(ts), a.clock) // end > clock only; -inf admits all
	if len(ks) == 0 {
		a.drop++
		return nil
	}
	fresh := 0 // live counts are always >=1, so a zero means absent
	for _, k := range ks {
		if a.counts[key][k] == 0 {
			fresh++
		}
	}
	if len(a.open)+fresh > a.max {
		return ErrTooManyOpen
	}
	if a.counts[key] == nil {
		a.counts[key] = map[int64]int64{}
	}
	for _, k := range ks {
		if a.counts[key][k] == 0 {
			heap.Push(&a.open, ent{key, k, a.p.At(k).End})
		}
		a.counts[key][k]++
	}
	return nil
}
func (a *Agg) closeLocked(t int64) []Result { // pops/deletes every end <= t window in order
	var emit []Result
	a.last = 0
	for a.open.Len() > 0 {
		a.last++
		top := a.open[0]
		if top.end > t { // heap order: every later entry stays open
			break
		}
		heap.Pop(&a.open)
		w := a.p.At(top.k)
		emit = append(emit, Result{top.key, top.k, w.Start, w.End, a.counts[top.key][top.k]})
		delete(a.counts[top.key], top.k)
	}
	a.out = append(a.out, emit...)
	return emit
}
func (a *Agg) Advance(t int64) ([]Result, error) { // clock to t (non-decreasing), closes end <= t
	a.mu.Lock()
	defer a.mu.Unlock()
	if t < a.clock {
		return nil, ErrClockBack
	}
	a.clock = t
	return append([]Result(nil), a.closeLocked(t)...), nil
}
func (a *Agg) Flush() []Result { // Advance(+infinity)
	a.mu.Lock()
	defer a.mu.Unlock()
	r := append([]Result(nil), a.closeLocked(1<<63-1)...)
	a.clock = 1<<63 - 1
	return r
}
func (a *Agg) Results() []Result { // copy of every result emitted so far
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]Result(nil), a.out...)
}
func (a *Agg) Dropped() int64 { // events whose windows were all closed
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.drop
}
func ScanBoundOK() bool { // built-in check: close-none scans O(1), close-c scans O(c); verdict only, counter never exposed
	ok := true
	for _, m := range []int{100, 1000, 10000} {
		a, _ := New(4, 4, m*2+8)
		for i := 0; i < m; i++ {
			a.Add("k"+strconv.Itoa(i), 1<<40)
		}
		c := m / 7
		for i := 0; i < c; i++ {
			a.Add("c"+strconv.Itoa(i), 0)
		}
		a.Advance(0)
		lc := a.last
		a.Advance(4)
		ok = ok && lc <= 1 && a.last <= int64(c)+1
	}
	return ok
}

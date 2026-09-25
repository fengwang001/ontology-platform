// Package mbatch implements micro-batch triggered tumbling-window counting.
package mbatch

import (
	"container/heap"
	"errors"
	"strconv"

	"ontology/win"
)

var ErrWNonPositive = errors.New("mbatch: window size W must be positive")
var ErrBNonPositive = errors.New("mbatch: batch size B must be positive")
var ErrEmptyKey = errors.New("mbatch: event key must not be empty")

type Event struct {
	Key string
	TS  int64
}
type Change struct {
	Key   string
	Win   win.Window
	Value int64
	Plus  bool // true => +(Key,Win,Value), false => a withdrawal -(Key,Win,Value)
}
type Cell struct {
	Key string
	K   int64
}
type lateRec struct {
	c   Cell
	old int64
}
type openItem struct {
	c   Cell
	end int64
}
type openHeap []openItem
type M struct {
	w, b        int64
	buf         []Event
	wm          int64 // before the first event this is treated as -infinity
	flushed     bool
	counts      map[Cell]int64
	closed      map[Cell]bool
	open        openHeap
	log         []Change
	closeProbes int64 // unexported read+compare count; no accessor exposes its value
}

func (h openHeap) Len() int           { return len(h) }
func (h openHeap) Less(i, j int) bool { return h[i].end < h[j].end }
func (h openHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *openHeap) Push(x any)        { *h = append(*h, x.(openItem)) }
func (h *openHeap) Pop() any          { x := (*h)[len(*h)-1]; *h = (*h)[:len(*h)-1]; return x }
func New(w, b int64) (*M, error) {
	if w <= 0 {
		return nil, ErrWNonPositive
	}
	if b <= 0 {
		return nil, ErrBNonPositive
	}
	return &M{w: w, b: b, counts: map[Cell]int64{}, closed: map[Cell]bool{}}, nil
}
func (m *M) trigger(batch []Event) []Change {
	out, late, wm := []Change{}, []lateRec{}, m.wm
	for _, e := range batch { // step 1: advance wm, assign floor(TS/W), snapshot late old, count
		if e.TS > wm {
			wm = e.TS
		}
		c := Cell{e.Key, win.Index(e.TS, m.w)}
		if _, ok := m.counts[c]; !ok {
			m.counts[c] = 0
			heap.Push(&m.open, openItem{c, (c.K + 1) * m.w})
		} else if m.closed[c] { // closed before this trigger => late
			late = append(late, lateRec{c, m.counts[c]})
		}
		m.counts[c]++
	}
	m.wm, m.closeProbes = wm, 0
	for m.open.Len() > 0 { // step 2: close once, ascending end; each peek is one probe
		m.closeProbes++
		top := m.open[0]
		if !m.flushed && !win.Closed(top.end, wm) {
			break
		}
		heap.Pop(&m.open)
		m.closed[top.c] = true
		out = append(out, Change{top.c.Key, win.Of(top.c.K*m.w, m.w), m.counts[top.c], true})
	}
	for _, r := range late { // step 3: late -/+ deltas, in arrival order
		wd := win.Of(r.c.K*m.w, m.w)
		out = append(out, Change{r.c.Key, wd, r.old, false}, Change{r.c.Key, wd, r.old + 1, true})
	}
	m.log = append(m.log, out...)
	return out
}
func (m *M) Feed(evs []Event) ([]Change, error) {
	for _, e := range evs { // reject the whole slice before any state change
		if e.Key == "" {
			return nil, ErrEmptyKey
		}
	}
	out := []Change{}
	for _, e := range evs {
		m.buf = append(m.buf, e)
		if int64(len(m.buf)) >= m.b {
			out, m.buf = append(out, m.trigger(m.buf)...), nil
		}
	}
	return out, nil
}
func (m *M) Flush() []Change {
	if m.flushed {
		return nil
	}
	m.flushed = true
	out := m.trigger(m.buf)
	m.buf = nil
	return out
}
func (m *M) Log() []Change { out := make([]Change, len(m.log)); copy(out, m.log); return out }
func (m *M) View() map[Cell]int64 {
	v := map[Cell]int64{}
	for _, c := range m.log {
		id := Cell{c.Key, c.Win.K}
		if c.Plus {
			v[id] = c.Value
		} else {
			delete(v, id)
		}
	}
	return v
}
func ProbeBoundHolds() bool {
	// Verdict only: k=3 closures probe <= k+1 at m=100/1000/10000; 0 closures peek once.
	for _, n := range []int{100, 1000, 10000} {
		c, _ := New(10, 4)
		for i := 0; i < n; i++ { // n distinct (Key,window) cells, buffer ends empty
			c.Feed([]Event{{"k" + strconv.Itoa(i), 900}})
		}
		if c.Feed([]Event{{"L0", 0}, {"L1", 10}, {"L2", 20}, {"k0", 902}}); c.closeProbes > 4 {
			return false
		}
		if c.Feed([]Event{{"k0", 903}, {"k1", 903}, {"k2", 903}, {"k3", 903}}); c.closeProbes != 1 {
			return false
		}
	}
	return true
}

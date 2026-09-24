package cagg

import "container/heap"
import "errors"
import "math"
import "ontology/cwin"

var ErrInvalidParams = cwin.ErrInvalidParams
var ErrTooManyWindows = errors.New("cagg: too many open large windows")
var ErrEmptyKey = errors.New("cagg: empty event key")

type Event struct {
	Key string
	TS  int64
}
type Out struct {
	Key               string
	Start, End, Count int64
}
type wkey struct {
	key string
	s   int64
}
type win struct {
	counts []int64
	next   int
}
type pqItem struct {
	end int64
	key wkey
}
type pq []pqItem
type Agg struct {
	max, step, delay, maxTS, drop int64
	n, maxOpen, scanCnt           int
	wins                          map[wkey]*win
	index                         pq
	outs                          []Out
}

func (p pq) Len() int      { return len(p) }
func (p pq) Swap(i, j int) { p[i], p[j] = p[j], p[i] }
func (p *pq) Push(x any)   { *p = append(*p, x.(pqItem)) }
func (p *pq) Pop() any     { x := (*p)[len(*p)-1]; *p = (*p)[:len(*p)-1]; return x }
func (p pq) Less(i, j int) bool {
	a, b := p[i], p[j]
	return a.end < b.end || a.end == b.end && a.key.key < b.key.key
}

func New(max, step, delay int64, maxOpen int) (*Agg, error) {
	if err := cwin.Validate(max, step, delay); err != nil {
		return nil, err
	}
	return &Agg{max: max, step: step, delay: delay, maxTS: math.MinInt64, n: cwin.SubCount(max, step), maxOpen: maxOpen, wins: map[wkey]*win{}}, nil
}
func (a *Agg) preflight(evs []Event) error {
	present, h := map[wkey]bool{}, pq(nil)
	for k := range a.wins {
		present[k] = true
		heap.Push(&h, pqItem{end: k.s + a.max, key: k})
	}
	maxTS := a.maxTS
	for _, e := range evs {
		if e.Key == "" {
			return ErrEmptyKey
		}
		maxTS = max(maxTS, e.TS)
		wm := maxTS - a.delay
		k := wkey{e.Key, cwin.Start(e.TS, a.max)}
		if em := cwin.End(k.s, a.step, cwin.MinSub(e.TS, k.s, a.step)); wm < em && !present[k] {
			present[k] = true
			heap.Push(&h, pqItem{end: k.s + a.max, key: k})
		}
		for h.Len() > 0 && h[0].end <= wm {
			delete(present, heap.Pop(&h).(pqItem).key)
		}
		if h.Len() > a.maxOpen {
			return ErrTooManyWindows
		}
	}
	return nil
}
func (a *Agg) Feed(evs []Event) ([]Out, error) {
	if err := a.preflight(evs); err != nil {
		return nil, err
	}
	var out []Out
	for _, e := range evs {
		out = append(out, a.feedOne(e)...)
	}
	return out, nil
}
func (a *Agg) feedOne(e Event) []Out {
	a.maxTS = max(a.maxTS, e.TS)
	wm := a.maxTS - a.delay
	s := cwin.Start(e.TS, a.max)
	jMin := cwin.MinSub(e.TS, s, a.step)
	if wm >= cwin.End(s, a.step, jMin) {
		a.drop++
	} else {
		k := wkey{e.Key, s}
		w := a.wins[k]
		if w == nil {
			w = &win{counts: make([]int64, a.n), next: 1}
			a.wins[k] = w
			heap.Push(&a.index, pqItem{end: cwin.End(s, a.step, 1), key: k})
		}
		for j := jMin; j <= a.n; j++ {
			w.counts[j-1]++
		}
	}
	return a.drain(wm)
}
func (a *Agg) drain(wm int64) []Out {
	out, touched := []Out(nil), map[wkey]struct{}{}
	for a.index.Len() > 0 && a.index[0].end <= wm {
		it := heap.Pop(&a.index).(pqItem)
		touched[it.key] = struct{}{}
		w := a.wins[it.key]
		out = append(out, Out{Key: it.key.key, Start: it.key.s, End: it.end, Count: w.counts[w.next-1]})
		w.next++
		if w.next > a.n {
			delete(a.wins, it.key)
		} else {
			heap.Push(&a.index, pqItem{end: cwin.End(it.key.s, a.step, w.next), key: it.key})
		}
	}
	a.scanCnt = len(touched)
	a.outs = append(a.outs, out...)
	return out
}
func (a *Agg) Flush() []Out   { return a.drain(math.MaxInt64) }
func (a *Agg) All() []Out     { cp := make([]Out, len(a.outs)); copy(cp, a.outs); return cp }
func (a *Agg) Dropped() int64 { return a.drop }
func VerifyScanBound() error {
	for _, m := range []int{100, 1000, 10000} {
		a, _ := New(1, 1, 1<<60, m+1)
		evs := make([]Event, m)
		for i := range evs {
			evs[i] = Event{Key: string([]byte{byte(i), byte(i >> 8)}), TS: int64(i)}
		}
		if _, e := a.Feed(evs); e != nil {
			return e
		}
		if _, e := a.Feed([]Event{{Key: "probe", TS: int64(m)}}); e != nil || a.scanCnt > 2 {
			return ErrTooManyWindows
		}
	}
	return nil
}

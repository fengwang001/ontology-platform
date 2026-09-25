package api

import (
	"errors"
	"maps"
	"math/rand"
	"ontology/dim"
	"ontology/join"
	"reflect"
	"sync"
)

var ErrInvalidMaxBytes = errors.New("api: maxBytes must be positive")

type Entry = dim.Entry
type Fact = join.Fact
type Row = join.Row
type API struct {
	mu         sync.RWMutex
	store      *dim.Store
	res        *join.Resolver
	rows       []Row
	drop, miss int64
}
type oracle struct {
	capB, v, used, oldest int64
	ms                    []map[string]int64
	sz                    []int64
	rows                  []Row
	drop, miss            int
}

func New(m int64) (*API, error) {
	if m <= 0 {
		return nil, ErrInvalidMaxBytes
	}
	s := dim.New(m)
	return &API{store: s, res: join.New(s)}, nil
}
func (a *API) Broadcast(es []Entry) (int64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.store.Broadcast(es)
}
func (a *API) Join(f Fact) (Row, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	r, k, e := a.res.Resolve(f)
	if e != nil {
		return Row{}, e
	}
	if k == join.Emitted {
		a.rows = append(a.rows, r)
		return r, nil
	}
	if k == join.Missed {
		a.miss++
	} else {
		a.drop++
	}
	return Row{}, nil
}
func (a *API) View() []Entry { a.mu.RLock(); defer a.mu.RUnlock(); return a.store.Current() }
func (a *API) Joined() []Row {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return append([]Row{}, a.rows...)
}
func (a *API) Dropped() int64 { a.mu.RLock(); defer a.mu.RUnlock(); return a.drop }
func (a *API) Missed() int64  { a.mu.RLock(); defer a.mu.RUnlock(); return a.miss }
func (o *oracle) cast(b []Entry) error {
	if len(b) == 0 {
		return dim.ErrEmptyBatch
	}
	m, by := maps.Clone(o.ms[o.v]), o.sz[o.v]
	for _, e := range b {
		if e.Key == "" {
			return dim.ErrEmptyKey
		} else if _, ok := m[e.Key]; !ok {
			by += int64(len(e.Key)) + 8
		}
		m[e.Key] = e.Val
	}
	u, old := o.used+by, o.oldest
	for u > o.capB && o.v+1-old >= 2 { // tentative; commit only on success
		u, old = u-o.sz[old], old+1
	}
	if u > o.capB {
		return dim.ErrTooBig
	}
	o.ms, o.sz = append(o.ms, m), append(o.sz, by)
	o.v, o.used, o.oldest = o.v+1, u, old
	return nil
}
func (o *oracle) join(f Fact) (Row, error) {
	if f.Key == "" {
		return Row{}, dim.ErrEmptyKey
	}
	if f.Vsn > o.v {
		return Row{}, dim.ErrFuture
	}
	if f.Vsn < o.oldest {
		o.drop++
		return Row{}, nil
	}
	if v, ok := o.ms[f.Vsn][f.Key]; ok {
		r := Row{Key: f.Key, Val: v, Vsn: f.Vsn}
		o.rows = append(o.rows, r)
		return r, nil
	}
	o.miss++
	return Row{}, nil
}
func (a *API) SelfCheck() error {
	g, _ := New(100)
	w, rnd := &oracle{capB: 100, ms: []map[string]int64{{}}, sz: []int64{0}}, rand.New(rand.NewSource(499))
	for i := 0; i < 400; i++ {
		kc := func() string { return string("abcdef"[rnd.Intn(6)]) }
		if rnd.Intn(2) == 0 {
			var b []Entry
			if r := rnd.Intn(10); r == 1 {
				b = []Entry{{Key: ""}}
			} else if r > 1 {
				for n := 1 + rnd.Intn(3); n > 0; n-- {
					b = append(b, Entry{Key: kc(), Val: rnd.Int63n(20)})
				}
			}
			gv, ge := g.Broadcast(b)
			if we := w.cast(b); !errors.Is(ge, we) || ge == nil && gv != w.v {
				return errors.New("selfcheck: broadcast divergence")
			}
			continue
		}
		f := Fact{Key: kc(), Vsn: rnd.Int63n(w.v + 3)}
		if x := rnd.Intn(12); x == 0 {
			f.Key = ""
		} else if x == 1 {
			f.Vsn = w.v + 1 + rnd.Int63n(3)
		}
		gr, ge := g.Join(f)
		wr, we := w.join(f)
		if !errors.Is(ge, we) || gr != wr {
			return errors.New("selfcheck: join divergence")
		}
	}
	if !reflect.DeepEqual([6]any{g.store.V(), g.store.Used(), g.store.Oldest(), g.Dropped(), g.Missed(), g.Joined()}, [6]any{w.v, w.used, w.oldest, int64(w.drop), int64(w.miss), w.rows}) {
		return errors.New("selfcheck: state divergence")
	}
	return nil
}

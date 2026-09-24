package thr

import (
	"errors"
	"maps"
	"sync"

	"ontology/deb"
)

type Refresh struct {
	Key string
	N   int64
	Val string
}

var ErrBadParam = errors.New("debounce: w and maxKeys must be positive")
var ErrEmptyKey = errors.New("debounce: key must not be empty")
var ErrClockBack = errors.New("debounce: logical time must not go backwards")
var ErrTooMany = errors.New("debounce: too many distinct pending keys")

type Throttler struct {
	mu                  sync.RWMutex
	w, max, last, fired int64
	probes              int64 // non-exported Tick probe count; white-box tests only
	pq                  []pend
	index               map[string]int
	view                map[string]string
}

type pend struct {
	key string
	b   deb.Batch
}

func New(w, maxKeys int64) (*Throttler, error) {
	if w <= 0 || maxKeys <= 0 {
		return nil, ErrBadParam
	}
	return &Throttler{w: w, max: maxKeys, index: map[string]int{}, view: map[string]string{}}, nil
}

// Record: all checks precede all mutation; rejection leaves no trace.
func (t *Throttler) Record(key string, at int64, val string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if key == "" {
		return ErrEmptyKey
	}
	if at < t.last {
		return ErrClockBack
	}
	i, open := t.index[key]
	if !open && int64(len(t.pq)) >= t.max {
		return ErrTooMany
	}
	t.last = at
	if open {
		t.pq[i].b.Merge(at, t.w, val) // postpones Due; sift down
		t.down(i)
	} else {
		t.push(pend{key, deb.NewBatch(at, t.w, val)})
	}
	return nil
}
func (t *Throttler) Tick(now int64) []Refresh { return t.flush(now, true) }
func (t *Throttler) Stop(now int64) []Refresh { return t.flush(now, false) }
func (t *Throttler) flush(now int64, due bool) []Refresh {
	t.mu.Lock()
	defer t.mu.Unlock()
	if now < t.last {
		return nil
	}
	if due {
		t.probes = 0
	}
	var out []Refresh
	for len(t.pq) > 0 {
		if due {
			t.probes++ // inspect heap root only: ordered lookup, never a scan
			if !t.pq[0].b.IsDue(now) {
				break
			}
		}
		out = append(out, t.fire(t.pop()))
	}
	t.last = now
	return out
}
func (t *Throttler) fire(p pend) Refresh {
	n, v := p.b.Take()
	t.view[p.key], t.fired = v, t.fired+1
	return Refresh{p.key, n, v}
}
func (t *Throttler) View() map[string]string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return maps.Clone(t.view)
}
func (t *Throttler) Fired() int64 { t.mu.RLock(); defer t.mu.RUnlock(); return t.fired }

// binary min-heap ordered by (Due asc, Key asc)
func (t *Throttler) less(i, j int) bool {
	a, b := t.pq[i], t.pq[j]
	return a.b.Due < b.b.Due || a.b.Due == b.b.Due && a.key < b.key
}
func (t *Throttler) swap(i, j int) {
	t.pq[i], t.pq[j] = t.pq[j], t.pq[i]
	t.index[t.pq[i].key], t.index[t.pq[j].key] = i, j
}
func (t *Throttler) push(p pend) {
	i := len(t.pq)
	t.pq, t.index[p.key] = append(t.pq, p), i
	for par := (i - 1) / 2; i > 0 && t.less(i, par); par = (i - 1) / 2 {
		t.swap(i, par)
		i = par
	}
}
func (t *Throttler) pop() pend {
	p := t.pq[0]
	delete(t.index, p.key)
	if n := len(t.pq) - 1; n > 0 {
		t.pq[0], t.index[t.pq[n].key] = t.pq[n], 0
		t.pq = t.pq[:n]
		t.down(0)
	} else {
		t.pq = t.pq[:0]
	}
	return p
}
func (t *Throttler) down(i int) {
	for {
		l, r, b := 2*i+1, 2*i+2, i
		if l < len(t.pq) && t.less(l, b) {
			b = l
		}
		if r < len(t.pq) && t.less(r, b) {
			b = r
		}
		if b == i {
			return
		}
		t.swap(i, b)
		i = b
	}
}

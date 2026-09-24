// Package store keeps live rows, version-ordered tombstones and watermark G.
package store

import (
	"container/heap"
	"errors"
	"maps"
	"slices"
	"strconv"
	"sync"

	"ontology/rule"
)

// Event is one CDC change; Op is 'U' (upsert) or 'D' (delete), Ver the LSN.
type Event struct {
	Op  byte
	Key string
	Val string
	Ver int64
}
type Row struct {
	Val string
	Ver int64
}

var ErrInvalidEvent = errors.New("store: invalid event")
var ErrTooManyKeys = errors.New("store: entry count exceeds maxKeys")

type tombEnt struct {
	ver int64
	key string
}
type tombHeap []tombEnt

func (h tombHeap) Len() int           { return len(h) }
func (h tombHeap) Less(i, j int) bool { return h[i].ver < h[j].ver }
func (h tombHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *tombHeap) Push(x any)        { *h = append(*h, x.(tombEnt)) }
func (h *tombHeap) Pop() (e any)      { n := len(*h) - 1; e = (*h)[n]; *h = (*h)[:n]; return }

// Store is the in-memory table, safe for concurrent use; unexported checked counts entries inspected by the latest purge.
type Store struct {
	mu               sync.RWMutex
	r, g, ignored    int64
	maxKeys, checked int
	rows             map[string]Row
	tombs            map[string]int64
	th               tombHeap
}

func New(r int64, maxKeys int) *Store {
	return &Store{r: r, maxKeys: maxKeys, rows: map[string]Row{}, tombs: map[string]int64{}}
}

// Batch applies evs atomically; an invalid event or an entry-count overflow
// rejects the whole batch, leaving no state change behind.
func (s *Store) Batch(evs []Event) error {
	for _, e := range evs {
		if (e.Op != 'U' && e.Op != 'D') || e.Key == "" || e.Ver <= 0 {
			return ErrInvalidEvent
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, tombs, th, g, ign, chk := s.rows, s.tombs, s.th, s.g, s.ignored, s.checked
	s.rows, s.tombs, s.th = maps.Clone(rows), maps.Clone(tombs), slices.Clone(th)
	for _, e := range evs {
		s.apply(e)
		if len(s.rows)+len(s.tombs) > s.maxKeys {
			s.rows, s.tombs, s.th, s.g, s.ignored, s.checked = rows, tombs, th, g, ign, chk
			return ErrTooManyKeys
		}
	}
	return nil
}

// apply follows the fixed order: cur, apply/ignore, bump G, purge.
func (s *Store) apply(e Event) {
	lr, live := s.rows[e.Key]
	tv, hasT := s.tombs[e.Key]
	if rule.Applies(e.Ver, rule.Current(live, lr.Ver, hasT, tv)) {
		if e.Op == 'U' {
			s.rows[e.Key] = Row{Val: e.Val, Ver: e.Ver}
			delete(s.tombs, e.Key)
		} else {
			delete(s.rows, e.Key)
			s.tombs[e.Key] = e.Ver
			heap.Push(&s.th, tombEnt{ver: e.Ver, key: e.Key})
		}
	} else {
		s.ignored++
	}
	s.g = max(s.g, e.Ver)
	s.purge()
}

// purge clears G-t>=R tombstones in version order, stopping at the first non-expired heap root.
func (s *Store) purge() {
	s.checked = 0
	for len(s.th) > 0 {
		top := s.th[0]
		s.checked++
		if !rule.Expired(s.g, top.ver, s.r) {
			break
		}
		heap.Pop(&s.th)
		if cur, ok := s.tombs[top.key]; ok && cur == top.ver {
			delete(s.tombs, top.key)
		}
	}
}

// GetMany takes one consistent same-instant snapshot of all named keys.
func (s *Store) GetMany(keys []string) map[string]Row {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]Row, len(keys))
	for _, k := range keys {
		if r, ok := s.rows[k]; ok {
			out[k] = r
		}
	}
	return out
}
func (s *Store) Tomb(k string) (v int64, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok = s.tombs[k]
	return
}
func (s *Store) Stats() (ignored, g int64) { s.mu.RLock(); defer s.mu.RUnlock(); return s.ignored, s.g }

// PurgeCostBound: after m tombstones, one event advancing G by 1 expires nothing, so purge inspection is O(1); the counter's value never leaves the package. m in {100,1000,10000}.
func PurgeCostBound() bool {
	for _, m := range []int{100, 1000, 10000} {
		s := New(1<<60, m+2)
		evs := make([]Event, 0, m)
		for i := 1; i <= m; i++ {
			evs = append(evs, Event{Op: 'D', Key: strconv.Itoa(i), Ver: int64(i)})
		}
		if s.Batch(evs) != nil {
			return false
		}
		if s.Batch([]Event{{Op: 'U', Key: "p", Val: "y", Ver: int64(m + 1)}}) != nil || s.checked > 1 {
			return false
		}
	}
	return true
}

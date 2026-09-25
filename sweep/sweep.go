// Package sweep manages many streams and enumerates dead ones by expiry
// time (lastHb + IdleWindow) using a min-heap, so Sweep never scans all
// streams. It depends on hb only.
package sweep

import (
	"container/heap"
	"errors"
	"sort"
	"sync"

	"ontology/hb"
)

// ErrEmptyID rejects operations with an empty stream id. It is distinct
// from hb.ErrNegativeTime and hb.ErrClockBack.
var ErrEmptyID = errors.New("sweep: empty stream id")

// entry is one heap item. exp = lastHb + hb.IdleWindow; dead iff exp < now.
// last lets Sweep discard superseded entries lazily.
type entry struct {
	exp  int64
	last int64
	id   string
}

type expHeap []entry

func (h expHeap) Len() int           { return len(h) }
func (h expHeap) Less(i, j int) bool { return h[i].exp < h[j].exp }
func (h expHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *expHeap) Push(x any)        { *h = append(*h, x.(entry)) }
func (h *expHeap) Pop() (old any)    { n := len(*h); old = (*h)[n-1]; *h = (*h)[:n-1]; return }

// ViewEntry is one stream's snapshot row.
type ViewEntry struct {
	ID   string
	Last int64
}

// Manager tracks many streams. Zero value is ready; use New.
type Manager struct {
	mu      sync.RWMutex
	streams map[string]*hb.Stream
	h       expHeap
	checked int // streams examined by the most recent Sweep; unexported on purpose
}

// New returns an empty Manager.
func New() *Manager { return &Manager{streams: map[string]*hb.Stream{}} }

// Heartbeat applies hb's monotonic rule and indexes the stream by expiry.
// Empty id and negative ts are rejected before any state changes.
func (m *Manager) Heartbeat(id string, ts int64) error {
	if id == "" {
		return ErrEmptyID
	}
	if ts < 0 {
		return hb.ErrNegativeTime
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.streams[id]
	if !ok {
		s = hb.NewStream()
	}
	if err := s.Observe(ts); err != nil {
		return err // stale: lastHb unchanged, no heap write
	}
	if !ok {
		m.streams[id] = s
	}
	heap.Push(&m.h, entry{exp: ts + hb.IdleWindow, last: ts, id: id})
	return nil
}

// Status classifies one stream at now. Absent streams report hb.Absent.
// Empty id, negative now and clock rollback are rejected with sentinels.
func (m *Manager) Status(id string, now int64) (hb.State, error) {
	if id == "" {
		return hb.Absent, ErrEmptyID
	}
	if now < 0 {
		return hb.Absent, hb.ErrNegativeTime
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.streams[id]
	if !ok {
		return hb.Absent, nil
	}
	return s.StateAt(now)
}

// Sweep returns the ids of all currently dead streams, sorted ascending.
// It examines only heap entries whose expiry < now, plus the one live entry
// that stops the scan; stale entries are discarded lazily. Dead entries are
// re-pushed so later sweeps still report them.
func (m *Manager) Sweep(now int64) ([]string, error) {
	if now < 0 {
		return nil, hb.ErrNegativeTime
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checked = 0
	var dead []entry
	for len(m.h) > 0 {
		top := m.h[0]
		m.checked++
		if top.exp >= now {
			break // not yet dead; heap order means nothing later is dead either
		}
		heap.Pop(&m.h)
		if s, ok := m.streams[top.id]; ok {
			if last, seen := s.Last(); seen && last == top.last {
				dead = append(dead, top) // genuinely dead: keep for re-push
			}
		} // else: superseded entry, discarded
	}
	for _, e := range dead {
		heap.Push(&m.h, e)
	}
	ids := make([]string, len(dead))
	for i, e := range dead {
		ids[i] = e.id
	}
	sort.Strings(ids)
	return ids, nil
}

// View returns a stable snapshot of all known streams, sorted by id.
func (m *Manager) View() []ViewEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ViewEntry, 0, len(m.streams))
	for id, s := range m.streams {
		last, _ := s.Last()
		out = append(out, ViewEntry{ID: id, Last: last})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Package api is the public FIRST_VALUE service. Dependency: api -> first -> evt.
package api

import (
	"errors"
	"sync"

	"ontology/evt"
	"ontology/first"
)

// Sentinel errors: mutually distinguishable.
var (
	ErrEmptyKey = errors.New("api: key must not be empty")
	ErrNotFound = errors.New("api: no active occurrence of (key, ts) to remove")
	ErrCapacity = errors.New("api: active event count would exceed maxEvents")
)

// Manager is the concurrency-safe public service.
type Manager struct {
	mu        sync.RWMutex
	s         *first.Set
	maxEvents int // <=0 means unlimited
}

// New creates a Manager capped at maxEvents active occurrences.
func New(maxEvents int) *Manager {
	return &Manager{s: first.NewSet(), maxEvents: maxEvents}
}

// Add inserts one occurrence; empty key / overflow is rejected up front.
func (m *Manager) Add(Key string, TS int64) error {
	if Key == "" {
		return ErrEmptyKey
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.maxEvents > 0 && m.s.Count() >= m.maxEvents {
		return ErrCapacity
	}
	m.s.Add(evt.Event{Key: Key, TS: TS})
	return nil
}

// Remove withdraws one occurrence; a missing one is rejected up front.
func (m *Manager) Remove(Key string, TS int64) error {
	if Key == "" {
		return ErrEmptyKey
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.s.Remove(evt.Event{Key: Key, TS: TS}) {
		return ErrNotFound
	}
	return nil
}

// First returns the current FIRST_VALUE; ok is false with no active events.
func (m *Manager) First() (Key string, TS int64, ok bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, found := m.s.First()
	if !found {
		return "", 0, false
	}
	return e.Key, e.TS, true
}

// Count returns active occurrences (with multiplicity).
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.s.Count()
}

// SelfCheck verifies the four invariants on fresh local instances (never
// the receiver, so it is concurrency-safe).
func (m *Manager) SelfCheck() bool {
	s := first.NewSet()
	ref := map[evt.Event]int{}
	naive := func() (evt.Event, bool) {
		var mn evt.Event
		have := false
		for e, c := range ref {
			if c > 0 && (!have || evt.Less(e, mn)) {
				mn, have = e, true
			}
		}
		return mn, have
	}
	match := func() bool {
		g, ok1 := s.First()
		w, ok2 := naive()
		return ok1 == ok2 && (!ok1 || g == w)
	}
	adds := []evt.Event{{Key: "a", TS: 5}, {Key: "b", TS: 5}, {Key: "a", TS: 3}, {Key: "a", TS: 3}}
	rms := []evt.Event{{Key: "a", TS: 3}, {Key: "a", TS: 3}, {Key: "a", TS: 5}, {Key: "b", TS: 5}}
	for _, e := range adds {
		s.Add(e)
		ref[e]++
		if !match() {
			return false
		}
	}
	for _, e := range rms {
		if !s.Remove(e) {
			return false
		}
		ref[e]--
		if !match() {
			return false
		}
	}
	if s.Count() != 0 {
		return false
	}
	triple := evt.Event{Key: "x", TS: 1} // multiset round-trip
	for i := 0; i < 3; i++ {
		s.Add(triple)
		ref[triple]++
	}
	if s.Count() != 3 || !match() {
		return false
	}
	for i := 0; i < 3; i++ {
		s.Remove(triple)
		ref[triple]--
	}
	if !match() {
		return false
	}
	c := New(8) // three distinct rejections, state untouched
	for i := int64(0); i < 8; i++ {
		if err := c.Add(string(rune('a'+i)), i); err != nil {
			return false
		}
	}
	n := c.Count()
	k, t, _ := c.First()
	if !errors.Is(c.Add("", 1), ErrEmptyKey) ||
		!errors.Is(c.Add("zz", 9), ErrCapacity) ||
		!errors.Is(c.Remove("zz", 9), ErrNotFound) {
		return false
	}
	k2, t2, ok := c.First()
	return c.Count() == n && ok && k2 == k && t2 == t
}

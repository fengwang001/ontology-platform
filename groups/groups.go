// Package groups manages many per-key groups, each with its own item set
// and top-K board. It owns validation: empty arguments, duplicate Add and
// removing a missing item are rejected before any state changes.
package groups

import (
	"errors"

	"ontology/topk"
)

// Rejected-operation sentinels, mutually distinct and matchable via errors.Is.
var (
	ErrEmpty     = errors.New("groups: empty key or itemID")
	ErrDuplicate = errors.New("groups: item already exists in group")
	ErrNotFound  = errors.New("groups: item not found in group")
)

// Manager keeps one ordered item set and top-K board per key.
type Manager struct {
	k  int
	gs map[string]*group
}

type group struct {
	items map[string]topk.Item
	board *topk.Group
}

// New returns a Manager whose groups hold boards of at most k items.
func New(k int) *Manager { return &Manager{k: k, gs: map[string]*group{}} }

func (m *Manager) get(key string) *group {
	g, ok := m.gs[key]
	if !ok {
		g = &group{items: map[string]topk.Item{}, board: topk.New(m.k)}
		m.gs[key] = g
	}
	return g
}

// Add inserts itemID with score into key's group. A duplicate ItemID is
// rejected with ErrDuplicate and leaves all state untouched.
func (m *Manager) Add(key, itemID string, score int) error {
	if key == "" || itemID == "" {
		return ErrEmpty
	}
	g := m.get(key)
	if _, ok := g.items[itemID]; ok {
		return ErrDuplicate
	}
	it := topk.Item{ItemID: itemID, Score: score}
	g.items[itemID] = it
	g.board.Add(it)
	return nil
}

// Remove deletes itemID from key's group. A missing item is rejected with
// ErrNotFound and leaves all state untouched.
func (m *Manager) Remove(key, itemID string) error {
	if key == "" || itemID == "" {
		return ErrEmpty
	}
	g, ok := m.gs[key]
	if !ok {
		return ErrNotFound
	}
	if _, ok := g.items[itemID]; !ok {
		return ErrNotFound
	}
	delete(g.items, itemID)
	g.board.Remove(itemID)
	return nil
}

// TopK returns the board of key in total order (nil for an unknown key).
func (m *Manager) TopK(key string) []topk.Item {
	if g, ok := m.gs[key]; ok {
		return g.board.Top()
	}
	return nil
}

// All returns every surviving item of key in total order.
func (m *Manager) All(key string) []topk.Item {
	if g, ok := m.gs[key]; ok {
		return g.board.All()
	}
	return nil
}

// Package groups manages many named top-K groups. It depends only on topk.
package groups

import "ontology/topk"

// Manager holds one top-K Set per group Key.
type Manager struct {
	k      int
	groups map[string]*topk.Set
}

// NewManager creates a manager whose groups each keep k items in-list.
func NewManager(k int) *Manager {
	return &Manager{k: k, groups: make(map[string]*topk.Set)}
}

// Add inserts an item into the named group, creating the group on first use.
// A duplicate ItemID in that group is topk.ErrDuplicate and changes nothing.
func (m *Manager) Add(key string, it topk.Item) error {
	s, ok := m.groups[key]
	if !ok {
		s = topk.NewSet(m.k)
		m.groups[key] = s
	}
	return s.Add(it)
}

// Remove deletes an item from the named group. An unknown group or ItemID is
// topk.ErrNotFound and changes nothing.
func (m *Manager) Remove(key, itemID string) error {
	s, ok := m.groups[key]
	if !ok {
		return topk.ErrNotFound
	}
	return s.Remove(itemID)
}

// TopK returns the named group's in-list items, best first; an unknown group
// yields an empty (never nil) slice.
func (m *Manager) TopK(key string) []topk.Item {
	s, ok := m.groups[key]
	if !ok {
		return []topk.Item{}
	}
	return s.Top()
}

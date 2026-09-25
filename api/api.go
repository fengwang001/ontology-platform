// Package api is the public, concurrency-safe face of the per-group top-K
// materialized view.
package api

import (
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"sort"
	"sync"

	"ontology/groups"
	"ontology/topk"
)

// Item is one board entry: {ItemID, Score}.
type Item = topk.Item

// Rejected-call sentinels. The four categories (non-positive k, empty
// argument, duplicate Add, missing Remove) are mutually distinct.
var (
	ErrNonPositiveK = errors.New("api: k must be positive")
	ErrEmpty        = groups.ErrEmpty
	ErrDuplicate    = groups.ErrDuplicate
	ErrNotFound     = groups.ErrNotFound
)

// View is a materialized per-group top-K view over the change stream.
type View struct {
	mu sync.RWMutex
	m  *groups.Manager
}

// New builds a view with board size k; k <= 0 is rejected.
func New(k int) (*View, error) {
	if k <= 0 {
		return nil, ErrNonPositiveK
	}
	return &View{m: groups.New(k)}, nil
}

// Add applies one {Key, ItemID, Score} change.
func (v *View) Add(key, itemID string, score int) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.m.Add(key, itemID, score)
}

// Remove deletes one item; a board hole is refilled from below the line.
func (v *View) Remove(key, itemID string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.m.Remove(key, itemID)
}

// TopK returns the board of key in total order. Safe for concurrent use.
func (v *View) TopK(key string) []Item {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.m.TopK(key)
}

// batch is the independent reference: sort all surviving items by the total
// order and take the first k.
func batch(items []Item, k int) []Item {
	s := append([]Item(nil), items...)
	sort.Slice(s, func(i, j int) bool { return topk.Less(s[i], s[j]) })
	if len(s) > k {
		s = s[:k]
	}
	return s
}

// SelfCheck verifies the four invariants on built-in sequences against fresh
// state. Nil means all hold. Safe for concurrent use: touches no shared state.
func (v *View) SelfCheck() error {
	// Reference sequence of section 3 (K=2): ties and refills pinned exactly.
	m := groups.New(2)
	steps := []struct {
		add   bool
		id    string
		score int
		want  []Item
	}{
		{true, "a", 10, []Item{{ItemID: "a", Score: 10}}},
		{true, "b", 20, []Item{{ItemID: "b", Score: 20}, {ItemID: "a", Score: 10}}},
		{true, "c", 15, []Item{{ItemID: "b", Score: 20}, {ItemID: "c", Score: 15}}},
		{true, "d", 15, []Item{{ItemID: "b", Score: 20}, {ItemID: "c", Score: 15}}},
		{true, "e", 20, []Item{{ItemID: "b", Score: 20}, {ItemID: "e", Score: 20}}},
		{false, "b", 0, []Item{{ItemID: "e", Score: 20}, {ItemID: "c", Score: 15}}},
		{false, "e", 0, []Item{{ItemID: "c", Score: 15}, {ItemID: "d", Score: 15}}},
	}
	for i, s := range steps {
		var err error
		if s.add {
			err = m.Add("g", s.id, s.score)
		} else {
			err = m.Remove("g", s.id)
		}
		if err != nil {
			return fmt.Errorf("step %d: %w", i+1, err)
		}
		if got := m.TopK("g"); !slices.Equal(got, s.want) {
			return fmt.Errorf("step %d: board %v, want %v", i+1, got, s.want)
		}
	}
	// Invariants 1-3: deterministic random ops vs an independent model.
	r := rand.New(rand.NewSource(1))
	m2 := groups.New(5)
	model := map[string]Item{}
	for i := 0; i < 3000; i++ {
		id := fmt.Sprintf("i%02d", r.Intn(40))
		if r.Intn(2) == 0 {
			it := Item{ItemID: id, Score: r.Intn(50)}
			if m2.Add("g", id, it.Score) == nil {
				model[id] = it
			}
		} else if m2.Remove("g", id) == nil {
			delete(model, id)
		}
	}
	all := make([]Item, 0, len(model))
	for _, it := range model {
		all = append(all, it)
	}
	if got, want := m2.TopK("g"), batch(all, 5); !slices.Equal(got, want) {
		return fmt.Errorf("invariant 1: board %v, batch %v", got, want)
	}
	if got, want := m2.All("g"), batch(all, len(all)); !slices.Equal(got, want) {
		return fmt.Errorf("invariant 2: order broken")
	}
	// Invariant 4: rejected ops leave no trace.
	before := m2.All("g")
	for _, err := range []error{
		m2.Add("", "x", 1), m2.Add("g", "", 1),
		m2.Add("g", all[0].ItemID, 999), m2.Remove("g", "no-such"),
	} {
		if err == nil {
			return fmt.Errorf("invariant 4: rejected op returned nil")
		}
	}
	if got := m2.All("g"); !slices.Equal(got, before) {
		return fmt.Errorf("invariant 4: state changed by rejected ops")
	}
	return nil
}

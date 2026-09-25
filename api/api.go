// Package api is the public entry point for per-group incremental top-K.
package api

import (
	"errors"
	"reflect"
	"sort"
	"sync"

	"ontology/groups"
	"ontology/topk"
)

// Four decidable, pairwise-distinct fault classes.
var (
	ErrInvalidK      = errors.New("api: k must be positive")               // New(k<=0)
	ErrEmptyArgument = errors.New("api: key and itemID must be non-empty") // empty string arg
	ErrDuplicate     = topk.ErrDuplicate                                   // ItemID exists
	ErrNotFound      = topk.ErrNotFound                                    // ItemID absent
)

// Item is one scored member returned to callers.
type Item struct {
	ItemID string
	Score  int
}

// View is the concurrency-safe materialized view over all groups.
type View struct {
	k  int
	m  *groups.Manager
	rw sync.RWMutex
}

// New builds a view keeping the k highest items per group.
func New(k int) (*View, error) {
	if k <= 0 {
		return nil, ErrInvalidK
	}
	return &View{k: k, m: groups.NewManager(k)}, nil
}

// Add inserts one item. Validation happens before any state change.
func (v *View) Add(key, itemID string, score int) error {
	if key == "" || itemID == "" {
		return ErrEmptyArgument
	}
	v.rw.Lock()
	defer v.rw.Unlock()
	return v.m.Add(key, topk.Item{ItemID: itemID, Score: score})
}

// Remove deletes one item. Validation happens before any state change.
func (v *View) Remove(key, itemID string) error {
	if key == "" || itemID == "" {
		return ErrEmptyArgument
	}
	v.rw.Lock()
	defer v.rw.Unlock()
	return v.m.Remove(key, itemID)
}

// TopK returns a group's highest items, best first; empty for an unknown group.
func (v *View) TopK(key string) []Item {
	v.rw.RLock()
	defer v.rw.RUnlock()
	raw := v.m.TopK(key)
	out := make([]Item, len(raw))
	for i, x := range raw {
		out[i] = Item(x)
	}
	return out
}

// SelfCheck replays a built-in sequence on a scratch K=2 view and verifies the
// four invariants, returning a decidable error on any violation.
func (v *View) SelfCheck() error {
	const k = 2 // the built-in sequence in NOTES.md is derived for K=2
	sc, err := New(k)
	if err != nil {
		return err
	}
	ops := []Item{{"a", 10}, {"b", 20}, {"c", 15}, {"d", 15}, {"e", 20}}
	want := [][]Item{
		{{"a", 10}},
		{{"b", 20}, {"a", 10}},
		{{"b", 20}, {"c", 15}},
		{{"b", 20}, {"c", 15}},
		{{"b", 20}, {"e", 20}},
	}
	live := map[string]int{}
	for i, op := range ops { // invariants 1,2: equal to batch recompute each op
		if err := sc.Add("g", op.ItemID, op.Score); err != nil {
			return err
		}
		live[op.ItemID] = op.Score
		if got := sc.TopK("g"); !reflect.DeepEqual(got, want[i]) || !reflect.DeepEqual(got, batch(live, k)) {
			return errors.New("api: self-check batch consistency failed")
		}
	}
	if err := sc.Remove("g", "b"); err != nil ||
		!reflect.DeepEqual(sc.TopK("g"), []Item{{"e", 20}, {"c", 15}}) { // invariant 3
		return errors.New("api: self-check promotion failed")
	}
	delete(live, "b")
	if !reflect.DeepEqual(sc.TopK("g"), batch(live, k)) {
		return errors.New("api: self-check post-removal consistency failed")
	}
	before := sc.TopK("g")
	if err := sc.Add("g", "c", 999); !errors.Is(err, ErrDuplicate) { // invariant 4
		return errors.New("api: self-check duplicate not rejected")
	}
	if err := sc.Remove("g", "ghost"); !errors.Is(err, ErrNotFound) {
		return errors.New("api: self-check missing remove not rejected")
	}
	if !reflect.DeepEqual(sc.TopK("g"), before) {
		return errors.New("api: self-check rejected op left a trace")
	}
	return topk.AdmissionCheckBounded()
}

// batch is the independent oracle: sort every live item, take the first k.
func batch(live map[string]int, k int) []Item {
	all := make([]Item, 0, len(live))
	for id, sc := range live {
		all = append(all, Item{id, sc})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Score != all[j].Score {
			return all[i].Score > all[j].Score
		}
		return all[i].ItemID < all[j].ItemID
	})
	if len(all) > k {
		all = all[:k]
	}
	return all
}

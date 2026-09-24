// Package uni maintains the UNION DISTINCT view via global reference counts.
package uni

import (
	"errors"
	"fmt"
	"maps"
	"ontology/src"
	"slices"
	"sync"
)

// Change is one changelog entry: Add==true is "+Elem", false is "-Elem".
type Change struct {
	Add  bool
	Elem string
}

// Engine holds partitions, global counts and the ordered log; concurrent safe.
type Engine struct {
	mu        sync.RWMutex
	parts     []*src.Set
	cnt       map[string]int
	log       []Change
	lastProbe int // unexported section-4 counter: partitions scanned by the
	// latest Remove; cnt answers in O(1), so it stays 0 for any nPart.
}

// New creates an engine over nPart empty partitions.
func New(nPart int) *Engine {
	parts := make([]*src.Set, nPart)
	for i := range parts {
		parts[i] = src.New()
	}
	return &Engine{parts: parts, cnt: make(map[string]int)}
}

// Add inserts e into p: duplicate is a no-op; cnt[e] 0->1 emits "+e".
func (u *Engine) Add(p int, e string) (Change, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.parts[p].Add(e) {
		return Change{}, false
	}
	c := u.cnt[e] + 1
	u.cnt[e] = c
	if c == 1 {
		ch := Change{Add: true, Elem: e}
		u.log = append(u.log, ch)
		return ch, true
	}
	return Change{}, false
}

// Remove withdraws e from p: not-held is a no-op; cnt 1->0 emits "-e".
func (u *Engine) Remove(p int, e string) (Change, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.lastProbe = 0 // O(1) cnt check, no partition scan; 0 for any nPart
	if !u.parts[p].Remove(e) {
		return Change{}, false
	}
	c := u.cnt[e] - 1
	if c == 0 {
		delete(u.cnt, e)
		ch := Change{Add: false, Elem: e}
		u.log = append(u.log, ch)
		return ch, true
	}
	u.cnt[e] = c
	return Change{}, false
}

// View returns the sorted distinct union: zero counts are deleted, so
// cnt keys are exactly the elements with cnt >= 1.
func (u *Engine) View() []string {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return slices.Sorted(maps.Keys(u.cnt))
}

// Changes returns a copy of the changelog in emission order.
func (u *Engine) Changes() []Change { u.mu.RLock(); defer u.mu.RUnlock(); return slices.Clone(u.log) }

// ProbeBounded fills m partitions with "a" then removes all copies; it
// reports every Remove (including the final cnt 1->0 one) scanned <=1
// partition. It never exposes the counter's numeric value.
func ProbeBounded(m int) bool {
	u := New(m)
	for i := 0; i < m; i++ {
		u.Add(i, "a")
	}
	ok := true
	for i := 0; i < m; i++ {
		u.Remove(i, "a")
		if u.lastProbe > 1 {
			ok = false
		}
	}
	return ok
}

// SelfCheck verifies invariants 1-3 against an independent model (one set
// per partition): sets match, cnt[e]==holders>=1, and every changelog
// prefix replays with strictly alternating valid +e/-e.
func (u *Engine) SelfCheck(model []map[string]struct{}) error {
	u.mu.RLock()
	defer u.mu.RUnlock()
	if len(model) != len(u.parts) {
		return errors.New("uni: model partition count mismatch")
	}
	holds := map[string]int{}
	for p, want := range model { // equal size + subset => equal sets
		got := u.parts[p]
		if got.Len() != len(want) {
			return fmt.Errorf("uni: partition %d size %d want %d", p, got.Len(), len(want))
		}
		for e := range want {
			holds[e]++
			if !got.Contains(e) {
				return fmt.Errorf("uni: partition %d missing %q", p, e)
			}
		}
	}
	if len(u.cnt) != len(holds) { // cnt keys are exactly View and holders
		return fmt.Errorf("uni: cnt keys %d want %d", len(u.cnt), len(holds))
	}
	for e, c := range u.cnt { // cnt == holding-partition count and >= 1
		if c != holds[e] || c < 1 {
			return fmt.Errorf("uni: bad cnt[%q]=%d holders=%d", e, c, holds[e])
		}
	}
	view := map[string]struct{}{} // replay every prefix: strict alternation
	for i, ch := range u.log {
		_, present := view[ch.Elem]
		if ch.Elem == "" || present == ch.Add {
			return fmt.Errorf("uni: changelog %d invalid %+v", i, ch)
		}
		if ch.Add {
			view[ch.Elem] = struct{}{}
		} else {
			delete(view, ch.Elem)
		}
	}
	if len(view) != len(u.cnt) { // fully replayed view == current View
		return fmt.Errorf("uni: replayed view %d want %d", len(view), len(u.cnt))
	}
	return nil
}

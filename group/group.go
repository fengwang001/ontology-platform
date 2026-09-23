// Package group implements a tree of cancellation groups.
package group

import "sync"

// Group is a node in a cancellation tree. The zero value is not usable;
// create groups with New.
type Group struct {
	mu       sync.Mutex
	parent   *Group
	children map[*Group]struct{}
	timers   map[interface{}]struct{}
	canceled bool
	visits   int
}

// New creates a group; parent may be nil for a root group.
func New(parent *Group) *Group {
	g := &Group{children: map[*Group]struct{}{}, timers: map[interface{}]struct{}{}}
	if parent != nil {
		parent.mu.Lock()
		parent.children[g] = struct{}{}
		parent.mu.Unlock()
		g.parent = parent
	}
	return g
}

// Track registers timer id with g; it reports false if g is canceled.
func (g *Group) Track(id interface{}) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.canceled {
		return false
	}
	g.timers[id] = struct{}{}
	return true
}

// Untrack removes timer id from g (used by Stop and after firing).
func (g *Group) Untrack(id interface{}) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.timers, id)
}

// Canceled reports whether g has been canceled.
func (g *Group) Canceled() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.canceled
}

// Cancel cascades cancellation through the whole subtree, calling stop for
// every tracked timer. It returns the number of timers handed to stop.
// Calling it twice is a no-op returning 0.
func (g *Group) Cancel(stop func(id interface{})) int {
	g.mu.Lock()
	if g.canceled {
		g.mu.Unlock()
		return 0
	}
	g.canceled = true
	g.visits = 0
	n := g.cancelLocked(stop)
	g.mu.Unlock()
	return n
}

func (g *Group) cancelLocked(stop func(id interface{})) int {
	n := 0
	for id := range g.timers {
		g.visits++
		n++
		stop(id)
	}
	g.timers = map[interface{}]struct{}{}
	for c := range g.children {
		g.visits++
		c.mu.Lock()
		c.canceled = true
		n += c.cancelLocked(stop)
		c.mu.Unlock()
	}
	return n
}

// Visits returns the number of timers plus subgroups visited by the most
// recent Cancel call on this group.
func (g *Group) Visits() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.visits
}

// Package group keeps the member set of one consumer group, validates and
// commits change batches atomically, and tracks the generation counter.
package group

import (
	"errors"
	"sort"
	"sync"

	"ontology/assign"
)

// Sentinel errors, one per rejection cause; all are distinct.
var (
	ErrEmptyID       = errors.New("group: empty member id")
	ErrDuplicate     = errors.New("group: duplicate member (already joined or repeated in batch)")
	ErrAbsent        = errors.New("group: member not in group")
	ErrTooManyMember = errors.New("group: member count exceeds maxMembers")
)

// Change is one membership change inside a batch.
type Change struct {
	Join bool
	ID   string
}

// Joining returns a Join change for id.
func Joining(id string) Change { return Change{Join: true, ID: id} }

// Leaving returns a Leave change for id.
func Leaving(id string) Change { return Change{Join: false, ID: id} }

// Group is the member set plus the assignment table. Safe for concurrent use.
type Group struct {
	mu         sync.Mutex
	maxMembers int
	members    map[string]struct{}
	gen        int
	st         *assign.State
}

// New creates an empty group over n partitions, capped at maxMembers members.
func New(n, maxMembers int) *Group {
	return &Group{
		maxMembers: maxMembers,
		members:    map[string]struct{}{},
		st:         assign.New(n),
	}
}

// Apply validates the whole batch against a scratch copy of the member set;
// on any rejection nothing changes. On success it commits the set, runs
// exactly one rebalance, bumps the generation and returns migrations.
func (g *Group) Apply(changes []Change) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	next := make(map[string]struct{}, len(g.members)+len(changes))
	for id := range g.members {
		next[id] = struct{}{}
	}
	seen := make(map[string]struct{}, len(changes))
	for _, c := range changes {
		if c.ID == "" {
			return 0, ErrEmptyID
		}
		if _, dup := seen[c.ID]; dup {
			return 0, ErrDuplicate
		}
		seen[c.ID] = struct{}{}
		_, in := next[c.ID]
		if c.Join {
			if in {
				return 0, ErrDuplicate
			}
			next[c.ID] = struct{}{}
		} else {
			if !in {
				return 0, ErrAbsent
			}
			delete(next, c.ID)
		}
	}
	if len(next) > g.maxMembers {
		return 0, ErrTooManyMember
	}
	ids := make([]string, 0, len(next))
	for id := range next {
		ids = append(ids, id)
	}
	mig := g.st.Rebalance(ids)
	g.members = next
	g.gen++
	return mig, nil
}

// Members returns the sorted member IDs.
func (g *Group) Members() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	ids := make([]string, 0, len(g.members))
	for id := range g.members {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Assignment returns each member's partitions, sorted ascending.
func (g *Group) Assignment() map[string][]int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.st.Held()
}

// Generation returns the number of successful rebalances so far.
func (g *Group) Generation() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.gen
}

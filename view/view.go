// Package view maintains per-group aggregate state incrementally over a change
// stream, with member-driven recompute for non-withdrawable aggregators.
// Every change flows through Apply -> Recompute -> Commit under one write
// lock, so readers never observe a partially updated group.
package view

import (
	"errors"
	"math"
	"sync"

	"ontology/agg"
	"ontology/change"
)

var (
	// ErrVersionRollback: version older than the already-applied version.
	ErrVersionRollback = errors.New("view: version rollback")
	// ErrMissingGroup: a change carried no group key.
	ErrMissingGroup = errors.New("view: missing group")
	// ErrNaN: a change carried a NaN value.
	ErrNaN = errors.New("view: nan value")
	// ErrUnknownID: delete/update referenced a record that is not present.
	ErrUnknownID = errors.New("view: unknown record id")
)

// Phase names for crash injection hooks.
const (
	PhaseApply     = "apply"
	PhaseRecompute = "recompute"
	PhaseCommit    = "commit"
)

type member struct {
	group string
	value float64
}

// GroupState is an immutable snapshot of one group's aggregates.
type GroupState struct {
	Group     string
	Values    map[string]float64
	MemberIDs []string
}

// Stats holds recompute counters for one aggregator.
type Stats struct {
	RecomputeCount int
	MembersVisited int
}

// View is the incremental aggregate view.
type View struct {
	mu sync.RWMutex

	factory func() []agg.Aggregator
	groups  map[string][]agg.Aggregator
	members map[string]member
	byGroup map[string]map[string]float64

	lastVersion uint64

	recompCount  map[string]int
	memberVisits map[string]int

	rejected int

	// Hook, if non-nil, is invoked during each phase and may panic with
	// CrashPanic to simulate a crash before publication completes.
	Hook func(phase string)
}

// CrashPanic simulates a process crash inside a phase.
type CrashPanic struct{ Phase string }

func (CrashPanic) Error() string { return "view: simulated crash" }

// New builds a View using the given aggregator factory.
func New(factory func() []agg.Aggregator) *View {
	return &View{
		factory:      factory,
		groups:       map[string][]agg.Aggregator{},
		members:      map[string]member{},
		byGroup:      map[string]map[string]float64{},
		recompCount:  map[string]int{},
		memberVisits: map[string]int{},
	}
}

// LastVersion returns the greatest applied version.
func (v *View) LastVersion() uint64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.lastVersion
}

// Rejected returns the count of rejected changes (rollback, missing group,
// NaN, unknown id).
func (v *View) Rejected() int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.rejected
}

// Stats returns recompute counters keyed by aggregator name.
func (v *View) Stats() map[string]Stats {
	v.mu.RLock()
	defer v.mu.RUnlock()
	out := make(map[string]Stats, len(v.recompCount))
	for name := range v.recompCount {
		out[name] = Stats{v.recompCount[name], v.memberVisits[name]}
	}
	return out
}

// reject marks one change rejected. Caller holds the write lock.
func (v *View) reject() { v.rejected++ }

func isNaN(f float64) bool { return math.IsNaN(f) }

// Validate checks entry-level invariants without touching state.
func Validate(c change.Change) error {
	if c.Group == nil || (c.Op == change.Update && c.NewGroup == nil) {
		return ErrMissingGroup
	}
	if isNaN(c.Value) || (c.Op == change.Update && isNaN(c.NewValue)) {
		return ErrNaN
	}
	return nil
}

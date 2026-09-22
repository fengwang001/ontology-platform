// Package view maintains grouped aggregates incrementally over a stream of
// base-table changes, with durable crash recovery through journal.
//
// Every accepted change moves through three phases while the view mutex
// is held, so readers never observe a half-updated group:
//
//	Apply      -- validate, record the phase point, mutate membership
//	Recompute  -- rebuild a group only when an aggregator demanded members
//	Commit     -- publish the new group map, bump the applied version and
//	              durable-append the change to the journal
//
// Recompute is the exception: Count and Sum never need it; Min/Max need it
// only when the unique extremum is deleted; DistinctCount needs it on every
// deletion. Counters in Stats make those decisions observable.
package view

import (
	"math"
	"sync"

	"ontology/agg"
	"ontology/change"
	"ontology/journal"
)

// Phase names a crash injection point inside one Submit.
type Phase string

const (
	PhaseApply     Phase = "apply"
	PhaseRecompute Phase = "recompute"
	PhasePreCommit Phase = "pre-commit"
)

// GroupResult is an immutable snapshot of one group's five aggregates.
// Values with Exists==false mean the group currently has no aggregate
// contribution (the group is removed altogether when its last member
// leaves, so empty groups never appear in Groups()).
type GroupResult struct {
	Count                float64
	CountExists          bool
	Sum                  float64
	SumExists            bool
	Min, Max             float64
	MinExists, MaxExists bool
	Distinct             float64
	DistinctExists       bool
}

// memberEntry tracks one live record: its value and current group.
type memberEntry struct {
	value float64
	group string
}

type groupState struct {
	members map[string]float64
	aggs    map[agg.Kind]agg.Aggregator
	// dirty marks aggregators that must be rebuilt before publish.
	dirty map[agg.Kind]bool
}

// View is the concurrent-safe incremental maintainer.
type View struct {
	mu sync.RWMutex

	journalPath string
	w           *journal.Writer

	// maxVersion is the greatest applied version; versionSeen supports
	// idempotent re-delivery of the exact same change.
	maxVersion  uint64
	versionSeen map[uint64]uint64 // version -> FNV-like fingerprint

	members map[string]memberEntry
	groups  map[string]*groupState

	rejected  uint64
	stats     Stats
	hook      func(Phase, change.Change) error
	tailClass string
}

// TailClass returns the journal truncation category observed during the
// last recovery ("" if the log was intact or absent).
func (v *View) TailClass() string {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.tailClass
}

// Stats exposes non-exported counters through a read-only snapshot.
// TriggersByKind and MembersByKind are broken down per aggregator so the
// withdrawal strategy of each is independently observable.
type Stats struct {
	TriggersByKind map[agg.Kind]int64
	MembersByKind  map[agg.Kind]int64
	Rejected       int64 // out-of-order/missing-group/NaN changes rejected
	Reapplied      int64 // idempotent duplicate-version deliveries
}

func validRow(r change.Row) bool {
	return r.GroupPresent && !math.IsNaN(r.Value)
}

func norm(v float64) float64 { return change.NormZero(v) }

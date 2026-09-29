// Package pitr implements a point-in-time recovery engine with timeline
// branching.
//
// Positions are abstract unit coordinates on a single numeric axis. A log
// segment covers a left-closed, right-open interval [Start, End).
package pitr

import "sync"

// Position is a unit coordinate on the shared WAL position axis.
type Position = int64

// TimelineID identifies a timeline. Timeline 1 is the root timeline.
type TimelineID = int64

// Logger receives a single structured log line per engine decision.
type Logger interface {
	Log(kind string, fields map[string]any)
}

// Timeline is one branch of history. It forks from Parent at Fork: positions
// strictly before Fork come from Parent, positions from Fork onward belong to
// this timeline.
type Timeline struct {
	ID     TimelineID
	Parent TimelineID
	Fork   Position
}

// Segment is an archived segment on timeline TLI covering [Start, End).
type Segment struct {
	TLI   TimelineID
	Start Position
	End   Position
	Value any
}

// Backup is a base backup taken on timeline TLI whose included history ends
// at Upper (exclusive).
type Backup struct {
	ID    string
	TLI   TimelineID
	Upper Position
}

// ReplayStep replays the clipped portion [Start, End) of one segment.
type ReplayStep struct {
	Seg   Segment
	Start Position
	End   Position
}

// Plan is a deterministic recovery plan.
type Plan struct {
	TargetTLI TimelineID
	Target    Position
	Inclusive bool
	End       Position
	Backup    Backup
	Steps     []ReplayStep
}

// Result is the outcome of an executed recovery.
type Result struct {
	NewTLI TimelineID
	Parent TimelineID
	Fork   Position
	Plan   Plan
	State  uint64
}

// Registry is the concurrency-safe archive catalog and recovery engine.
type Registry struct {
	log Logger

	mu        *sync.RWMutex
	timelines map[TimelineID]Timeline
	segments  map[TimelineID][]Segment
	backups   []Backup
	maxTLI    TimelineID
}

// Snapshot is a read-only view used by tests.
type Snapshot struct {
	Timelines map[TimelineID]Timeline
	Segments  map[TimelineID][]Segment
	Backups   []Backup
	MaxTLI    TimelineID
}

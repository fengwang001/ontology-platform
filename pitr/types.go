// Package pitr implements point-in-time recovery with timeline branching.
package pitr

import "errors"

// Position is a log position (LSN-like integer).
type Position = int64

// TimelineID identifies a timeline. Timeline 1 is the initial timeline.
type TimelineID = int

const (
	// InitialTimeline is the root timeline; it has no parent.
	InitialTimeline TimelineID = 1
)

// TargetMode controls whether the recovery target position is replayed.
type TargetMode int

const (
	// ExcludeTarget replays positions p with backupEnd <= p < target.
	ExcludeTarget TargetMode = iota
	// IncludeTarget replays positions p with backupEnd <= p <= target.
	IncludeTarget
)

// Timeline is a registered timeline: every non-root timeline forked from
// Parent at Fork (positions < Fork belong to Parent's history).
type Timeline struct {
	ID     TimelineID
	Parent TimelineID
	Fork   Position
}

// Segment is an archived segment covering [Start, End) on Timeline.
type Segment struct {
	Timeline TimelineID
	Start    Position
	End      Position
}

// Backup is a base backup taken on Timeline, containing every position
// strictly below End.
type Backup struct {
	Timeline TimelineID
	End      Position
}

// PlanStep replays one archived segment (clipped to the required range)
// after the chosen base backup.
type PlanStep struct {
	Timeline TimelineID
	Start    Position
	End      Position
}

// Plan is a deterministic recovery plan.
type Plan struct {
	TargetTimeline TimelineID
	Target         Position
	Mode           TargetMode
	ReplayEnd      Position
	Backup         Backup
	Steps          []PlanStep
}

// Result is the outcome of Recover.
type Result struct {
	Plan        Plan
	NewTimeline Timeline
}

var (
	// ErrTimelineExists is returned when registering a timeline ID twice.
	ErrTimelineExists = errors.New("pitr: timeline already exists")
	// ErrParentNotFound is returned when the parent timeline is unknown
	// (including attempted registration of a non-1 root timeline).
	ErrParentNotFound = errors.New("pitr: parent timeline not found")
	// ErrForkBeforeParentFork is returned when a timeline forks strictly
	// before its parent's own fork position.
	ErrForkBeforeParentFork = errors.New("pitr: fork position is before parent fork position")
	// ErrInvalidSegment is returned when a segment range is empty or invalid.
	ErrInvalidSegment = errors.New("pitr: invalid segment range")
	// ErrInvalidBackup is returned when a backup bound is negative.
	ErrInvalidBackup = errors.New("pitr: invalid backup bound")
	// ErrTimelineNotFound is the first planning error: unknown target timeline.
	ErrTimelineNotFound = errors.New("pitr: timeline not found")
	// ErrBeyondArchive is the second planning error: target past archived frontier.
	ErrBeyondArchive = errors.New("pitr: target beyond archived log frontier")
	// ErrNoBackup is the third planning error: no usable base backup.
	ErrNoBackup = errors.New("pitr: no usable base backup")
	// ErrLogGap wraps a gap; errors.Is(err, ErrLogGap) detects it.
	ErrLogGap = errors.New("pitr: gap in archived log")
)

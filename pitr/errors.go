package pitr

import "errors"

var (
	// ErrTimelineNotFound is reported when the target timeline is not registered.
	ErrTimelineNotFound = errors.New("pitr: target timeline does not exist")
	// ErrTargetBeyondArchive is reported when the target lies past the archived end.
	ErrTargetBeyondArchive = errors.New("pitr: target beyond archived end")
	// ErrNoBackup is reported when no usable base backup exists.
	ErrNoBackup = errors.New("pitr: no usable base backup")
	// ErrGap is reported with the first missing position when the WAL chain has a hole.
	ErrGap = errors.New("pitr: gap in archived log")
	// ErrInvalidFork is reported when a timeline forks before its parent's own fork.
	ErrInvalidFork = errors.New("pitr: fork position precedes parent fork")
	// ErrInvalidSegment is reported for malformed or overlapping segments.
	ErrInvalidSegment = errors.New("pitr: invalid segment interval")
	// ErrInvalidBackup is reported for malformed base backups.
	ErrInvalidBackup = errors.New("pitr: invalid backup")
)

// GapError carries the first position that no segment of the effective
// timeline covers.
type GapError struct {
	Position Position
}

func (e *GapError) Error() string { return "pitr: gap in archived log" }

func (e *GapError) Unwrap() error { return ErrGap }

package snapshot

import "errors"

// Distinct, distinguishable rejection reasons. Callers can use errors.Is to
// tell the categories apart.
var (
	// ErrInvalidArgument is returned for nil inputs, negative retain counts
	// and malformed file names.
	ErrInvalidArgument = errors.New("snapshot: invalid argument")

	// ErrTimestampNotIncreasing is returned when a commit timestamp is not
	// strictly greater than the current snapshot timestamp.
	ErrTimestampNotIncreasing = errors.New("snapshot: timestamp must be strictly increasing")

	// ErrFileNameConflict is returned when a single commit lists a file more
	// than once or lists the same file as both added and removed.
	ErrFileNameConflict = errors.New("snapshot: file name conflict within commit")

	// ErrFileNameReused is returned when an added file name has already been
	// used by any snapshot in the history of the table, even if that snapshot
	// has since expired.
	ErrFileNameReused = errors.New("snapshot: added file name has been used before and must never be reused")

	// ErrRemoveNotFound is returned when a removed file is not present in the
	// current snapshot file set.
	ErrRemoveNotFound = errors.New("snapshot: removed file is not in the current snapshot")

	// ErrSnapshotNotFound is returned when querying an unknown snapshot id.
	ErrSnapshotNotFound = errors.New("snapshot: snapshot not found")

	// ErrFileNotFound is returned when querying a file that is not stored.
	ErrFileNotFound = errors.New("snapshot: data file not found")

	// ErrInvariantViolation is returned by CheckInvariants when the internal
	// state violates one of the documented consistency guarantees.
	ErrInvariantViolation = errors.New("snapshot: invariant violation")
)

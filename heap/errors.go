package heap

import "errors"

var (
	// 构造
	ErrInvalidPageCount = errors.New("heap: page count must be >= 1")
	ErrInvalidSlotCount = errors.New("heap: slots per page must be >= 1")

	// Insert
	ErrInvalidXid = errors.New("heap: xid must be a positive integer")
	ErrNoFreeSlot = errors.New("heap: no free slot on any page")

	// Delete
	ErrSlotOutOfRange = errors.New("heap: page or slot out of range")
	ErrSlotEmpty      = errors.New("heap: slot is empty")
	ErrAlreadyDeleted = errors.New("heap: row already has xmax")

	// Snapshot
	ErrInvalidSnapshotValue = errors.New("heap: snapshot value must be positive")
	ErrSnapshotTooOld       = errors.New("heap: snapshot value below global max used horizon")

	// Release / Scan
	ErrUnknownSnapshot = errors.New("heap: unknown snapshot id")

	// Vacuum
	ErrInvalidHorizon   = errors.New("heap: horizon must be positive")
	ErrPageOutOfRange   = errors.New("heap: page out of range")
	ErrSnapshotBlocking = errors.New("heap: an unreleased snapshot with value < h exists")

	// Scan
	ErrInvalidRange = errors.New("heap: lo must be <= hi")
)

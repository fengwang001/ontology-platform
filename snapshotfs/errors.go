package snapshotfs

import "errors"

// 可通过 errors.Is 区分的拒绝原因。
var (
	ErrInvalidArgument = errors.New("snapshotfs: invalid argument")
	ErrOutOfSpace      = errors.New("snapshotfs: out of space")
	ErrNotFound        = errors.New("snapshotfs: block not found")
	ErrDiscarded       = errors.New("snapshotfs: block discarded")
	ErrDead            = errors.New("snapshotfs: block already dead")
	ErrSnapshotExists  = errors.New("snapshotfs: snapshot already exists")
	ErrSnapshotMissing = errors.New("snapshotfs: snapshot not found")
	ErrNotHeld         = errors.New("snapshotfs: snapshot not held")
	ErrHeld            = errors.New("snapshotfs: snapshot is held")
)

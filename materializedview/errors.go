package materializedview

import "errors"

var (
	// ErrInvalidBlockSize means the requested chunk size is not positive.
	ErrInvalidBlockSize = errors.New("materializedview: block size must be greater than zero")
	// ErrRebuildAlreadyOpen means a rebuild is already active.
	ErrRebuildAlreadyOpen = errors.New("materializedview: rebuild is already in progress")
	// ErrNoRebuild means the method requires an active rebuild.
	ErrNoRebuild = errors.New("materializedview: no rebuild is in progress")
	// ErrRebuildIncomplete means a commit was attempted before all chunks were processed.
	ErrRebuildIncomplete = errors.New("materializedview: rebuild has not processed all events")
)

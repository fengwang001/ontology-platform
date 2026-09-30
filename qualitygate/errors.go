package qualitygate

import "errors"

// Distinguishable rejection reasons. None of these operations mutate state.
var (
	// ErrPaused is returned for Submit/Resubmit while the channel is paused.
	ErrPaused = errors.New("qualitygate: channel paused")
	// ErrInvalidBatch covers non-positive seq, negative rows, negative nulls
	// or nulls greater than rows.
	ErrInvalidBatch = errors.New("qualitygate: invalid batch")
	// ErrSeqExists is returned by Submit when the seq was already seen.
	ErrSeqExists = errors.New("qualitygate: seq already exists")
	// ErrSeqNotFound is returned when the seq has never been submitted.
	ErrSeqNotFound = errors.New("qualitygate: seq not found")
	// ErrNotQuarantined is returned when the seq is known but not in
	// quarantine (already passed, manually released, or discarded).
	ErrNotQuarantined = errors.New("qualitygate: batch not in quarantine")
	// ErrAlreadyPassed is wrapped by ErrNotQuarantined for passed batches.
	ErrAlreadyPassed = errors.New("qualitygate: batch already passed")
	// ErrAlreadyDiscarded is wrapped by ErrNotQuarantined for discarded ones.
	ErrAlreadyDiscarded = errors.New("qualitygate: batch already discarded")
	// ErrNotPaused is returned by Resume when the channel is not paused.
	ErrNotPaused = errors.New("qualitygate: channel not paused")
)

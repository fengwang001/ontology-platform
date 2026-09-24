// Package trunc implements two-phase, checkpoint-consistent truncation on top
// of package log.
//
// A truncation happens in two durable steps: first the truncation marker tm is
// written ("K will be removed"), then the prefix [0, K) is physically deleted.
// A crash between the two leaves tm ahead of the actual first offset f, which
// recovery detects and converges. The reverse order (delete before marker) is
// exactly what must never happen: it would produce f > tm, an unrecoverable
// hole. This package depends only on ontology/log.
package trunc

import (
	"errors"
	"sync"

	"ontology/log"
)

var (
	// ErrTruncateBeyondCheckpoint is returned when Truncate(k) would remove an
	// entry whose offset is >= cp, i.e. one that is not known to be persisted.
	ErrTruncateBeyondCheckpoint = errors.New("trunc: K beyond persistence checkpoint")
	// ErrRecoverOverDeletion is returned by Recover when the actual first
	// offset is past the marker (f > tm): entries were deleted without a
	// marker authorizing the deletion and are gone for good.
	ErrRecoverOverDeletion = errors.New("trunc: actual start beyond marker, entries lost")
)

// Truncator owns the truncation marker and performs two-phase truncation.
type Truncator struct {
	mu sync.Mutex
	lg *log.Log

	tm      uint64
	tmValid bool

	// metaReads counts metadata fields read while validating the most recent
	// Truncate call. Deciding K <= cp reads exactly one field (the persistence
	// point); it never scales with log length. Unexported on purpose.
	metaReads int
}

func New(lg *log.Log) *Truncator { return &Truncator{lg: lg} }

// Truncate removes [0, k). Legal iff k <= cp. The marker is persisted before
// any entry is touched, so a crash in the middle is recoverable.
func (t *Truncator) Truncate(k uint64) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.metaReads = 0
	cp, ok := t.lg.CP()
	t.metaReads++ // the persistence point is a single metadata field
	if !ok || k > cp {
		return ErrTruncateBeyondCheckpoint
	}
	t.tm, t.tmValid = k, true // phase 1: durable marker
	if _, err := t.lg.DropBefore(k); err != nil {
		return err
	} // phase 2: physical deletion
	return nil
}

// markOnly performs phase 1 and then crashes on purpose (no physical delete),
// reproducing a failure between marker write and deletion. Unexported: only
// white-box tests may inject this crash point.
func (t *Truncator) markOnly(k uint64) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.metaReads = 0
	cp, ok := t.lg.CP()
	t.metaReads++
	if !ok || k > cp {
		return ErrTruncateBeyondCheckpoint
	}
	t.tm, t.tmValid = k, true
	return nil
}

// maxCheckpointMetaReads is the provable upper bound on metadata fields read
// to decide one Truncate's legality. The persistence point is one field, so
// the bound is a constant independent of log length.
const maxCheckpointMetaReads = 1

// VerifyCheckpointCostConstant checks, at several log sizes ms, that deciding
// a legal Truncate never reads more than a constant number of metadata fields.
// It exposes only a pass/fail conclusion: the counter's numeric value never
// crosses this (or any) exported API, by design.
func VerifyCheckpointCostConstant(ms ...uint64) error {
	for _, m := range ms {
		lg := log.New()
		for i := uint64(0); i <= m; i++ {
			if _, err := lg.Append("x"); err != nil {
				return err
			}
		}
		if err := lg.Checkpoint(m); err != nil {
			return err
		}
		t := New(lg)
		k := m / 2 // any legal K; never exceeds cp
		if err := t.Truncate(k); err != nil {
			return err
		}
		if t.metaReads > maxCheckpointMetaReads { // white-box read, stays in-package
			return errors.New("trunc: legality check scanned entries instead of one field")
		}
	}
	return nil
}

// Marker returns the persisted truncation marker, if one was ever written.
func (t *Truncator) Marker() (uint64, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.tm, t.tmValid
}

// Recover is called at startup with the two numbers read back from storage:
// marker (tm) and the actual first offset (f).
//
//	f == tm: clean shutdown.
//	f <  tm: truncation interrupted; finish deleting [f, tm), converge f to tm.
//	f >  tm: over-deletion; entries in [tm, f) are lost -> ErrRecoverOverDeletion.
//
// A rejected recovery changes no state.
func (t *Truncator) Recover(marker, first uint64) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch {
	case first > marker:
		return ErrRecoverOverDeletion
	case first == marker:
		t.tm, t.tmValid = marker, true
		return nil
	default: // first < marker: complete the interrupted physical deletion
		if _, err := t.lg.DropBefore(marker); err != nil {
			return err
		}
		t.tm, t.tmValid = marker, true
		return nil
	}
}

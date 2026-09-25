// Package ckp maintains the single-stream position state (applied, flushed,
// flushTotal, ckpt) and its advancement rules. It depends on nothing.
package ckp

import "errors"

// Sentinel errors: distinguishable via errors.Is, mutually distinct.
var (
	ErrNonPositive  = errors.New("ckp: position must be positive")
	ErrGap          = errors.New("ckp: position not contiguous")
	ErrNoCheckpoint = errors.New("ckp: no checkpoint to restart from")
)

// Ckpt is a durable recovery point (pos, total); Valid=false means none.
type Ckpt struct {
	Pos   int64
	Total int64
	Valid bool
}

// State is the mutable single-stream state. Not goroutine-safe by itself.
type State struct {
	total      int64
	applied    int64
	flushed    int64
	flushTotal int64
	ckpt       Ckpt
}

// Apply requires pos == applied+1 (and pos > 0); on rejection nothing changes.
func (s *State) Apply(pos, delta int64) error {
	if pos <= 0 {
		return ErrNonPositive
	}
	if pos != s.applied+1 {
		return ErrGap
	}
	s.total += delta
	s.applied = pos
	return nil
}

// Flush marks all applied effects as durable and snapshots the current total.
func (s *State) Flush() {
	s.flushed = s.applied
	s.flushTotal = s.total
}

// Checkpoint writes (flushed, flushTotal) only when flushed has advanced
// past the existing checkpoint; otherwise it is a no-op (never regresses,
// never goes beyond flushed).
func (s *State) Checkpoint() {
	if !s.ckpt.Valid || s.flushed > s.ckpt.Pos {
		s.ckpt = Ckpt{Pos: s.flushed, Total: s.flushTotal, Valid: true}
	}
}

// Restart resets memory to the persistent checkpoint. With no checkpoint it
// fails without touching any state.
func (s *State) Restart() (Ckpt, error) {
	if !s.ckpt.Valid {
		return Ckpt{}, ErrNoCheckpoint
	}
	s.total = s.ckpt.Total
	s.applied = s.ckpt.Pos
	s.flushed = s.ckpt.Pos
	s.flushTotal = s.ckpt.Total
	return s.ckpt, nil
}

func (s *State) Total() int64      { return s.total }
func (s *State) Applied() int64    { return s.applied }
func (s *State) Flushed() int64    { return s.flushed }
func (s *State) FlushTotal() int64 { return s.flushTotal }
func (s *State) Ckpt() Ckpt        { return s.ckpt }

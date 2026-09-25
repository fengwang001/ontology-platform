// Package wm maintains the applied sequence, watermark and heartbeat clock,
// guaranteeing they only move forward. It depends on no other package.
package wm

import "errors"

// Sentinel errors are decidable with errors.Is and stay distinct from each
// other and from the errors declared by the stale and api packages.
var (
	// ErrDataGap: a Data record arrived whose Seq is not applied+1.
	ErrDataGap = errors.New("wm: data seq is not contiguous (want applied+1)")
	// ErrWatermarkBacktrack: UpTo is below the current watermark.
	ErrWatermarkBacktrack = errors.New("wm: watermark cannot move backwards")
)

// State holds A (last applied data seq), W (watermark) and lastHB (logical
// clock at the most recent heartbeat). All three start at zero.
type State struct {
	applied int64 // A
	mark    int64 // W
	lastHB  int64
}

// Applied returns A.
func (s *State) Applied() int64 { return s.applied }

// Mark returns W.
func (s *State) Mark() int64 { return s.mark }

// LastBeat returns lastHB.
func (s *State) LastBeat() int64 { return s.lastHB }

// Apply applies a data event. Seq must be exactly applied+1; the state is
// left completely untouched on rejection ("failure leaves no trace").
func (s *State) Apply(seq int64) error {
	if seq != s.applied+1 {
		return ErrDataGap
	}
	s.applied = seq
	return nil
}

// Raise advances the watermark to max(W, upTo). upTo < W is a rejected
// regression; upTo == W is accepted and changes nothing.
func (s *State) Raise(upTo int64) error {
	if upTo < s.mark {
		return ErrWatermarkBacktrack
	}
	if upTo > s.mark {
		s.mark = upTo
	}
	return nil
}

// Beat records a heartbeat taken at logical clock t. Callers (the stale
// package) only ever pass the monotonic current clock, so lastHB never
// regresses: it is always the clock at a past-or-present instant.
func (s *State) Beat(t int64) { s.lastHB = t }

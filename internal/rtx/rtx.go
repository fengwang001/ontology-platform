// Package rtx drives timer events (Send/Ack/Tick) on top of rto.State.
// It is the only place that validates events; every check happens before
// any state is touched, so a rejected event leaves no trace whatsoever.
package rtx

import (
	"errors"

	"ontology/internal/rto"
)

// Distinct sentinel errors; the api package re-exports these.
var (
	ErrInvalidRTO    = errors.New("rto: baseRTO must be positive")
	ErrSeqOutOfOrder = errors.New("rto: send seq must be strictly increasing")
	ErrNegativeAck   = errors.New("rto: ack must not be negative")
	ErrClockRollback = errors.New("rto: logical clock must not move backwards")
)

// Engine validates events and forwards them to the underlying rto.State.
type Engine struct {
	st *rto.State

	lastNow  int64
	haveNow  bool
	lastSeq  int64
	haveSent bool
}

// New creates an Engine with the given initial RTO.
func New(baseRTO int64) (*Engine, error) {
	if baseRTO <= 0 {
		return nil, ErrInvalidRTO
	}
	return &Engine{st: rto.New(baseRTO)}, nil
}

// checkClock rejects a now older than the previous event's now.
func (e *Engine) checkClock(now int64) error {
	if e.haveNow && now < e.lastNow {
		return ErrClockRollback
	}
	return nil
}

// commitClock records a now known to be non-decreasing.
func (e *Engine) commitClock(now int64) {
	e.lastNow, e.haveNow = now, true
}

// Send validates clock and strict seq increase, then registers the segment.
func (e *Engine) Send(seq, now int64) error {
	if err := e.checkClock(now); err != nil {
		return err
	}
	if e.haveSent && seq <= e.lastSeq {
		return ErrSeqOutOfOrder
	}
	e.commitClock(now)
	e.lastSeq, e.haveSent = seq, true
	e.st.Send(seq, now)
	return nil
}

// Ack validates clock and a non-negative cumulative ACK, then applies it.
func (e *Engine) Ack(a, now int64) error {
	if err := e.checkClock(now); err != nil {
		return err
	}
	if a < 0 {
		return ErrNegativeAck
	}
	e.commitClock(now)
	e.st.ApplyAck(a, now)
	return nil
}

// Tick validates the clock and performs one timeout check.
func (e *Engine) Tick(now int64) (timeout bool, retransmitted int64, err error) {
	if err := e.checkClock(now); err != nil {
		return false, 0, err
	}
	e.commitClock(now)
	to, seq := e.st.Probe(now)
	return to, seq, nil
}

// State exposes the underlying state for read-only accessors.
func (e *Engine) State() *rto.State { return e.st }

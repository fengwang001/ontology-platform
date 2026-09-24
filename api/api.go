// Package api is the external entry point. It depends only on hist and
// exposes the operations and the four distinct sentinel errors.
package api

import "ontology/hist"

// Event describes one event; it is the hist event type.
type Event = hist.Event

// The four rejection classes are distinct values; use errors.Is.
var (
	ErrNode        = hist.ErrNode
	ErrNoMessage   = hist.ErrNoMessage
	ErrAlreadyRecv = hist.ErrAlreadyRecv
	ErrLimit       = hist.ErrLimit
)

// System runs n in-memory nodes capped at maxEvents total events.
type System struct{ h *hist.History }

// New creates a System; n or maxEvents non-positive is ErrNode.
func New(n, maxEvents int) (*System, error) {
	h, err := hist.New(n, maxEvents)
	if err != nil {
		return nil, err
	}
	return &System{h: h}, nil
}

// Local performs a local event and returns its timestamp.
func (s *System) Local(node int) (int64, error) {
	e, err := s.h.Local(node)
	return e.TS, err
}

// Send performs a send from->to and returns the unique message ID, the
// send timestamp and an error. to == from is allowed.
func (s *System) Send(from, to int) (msgID int, ts int64, err error) {
	e, id, err := s.h.Send(from, to)
	return id, e.TS, err
}

// Recv receives msgID once on its target node and returns the timestamp.
func (s *System) Recv(msgID int) (int64, error) {
	e, err := s.h.Recv(msgID)
	return e.TS, err
}

// Order returns all events in the (timestamp, nodeID) total order.
func (s *System) Order() []Event { return s.h.Order() }

// HappensBefore reports whether event ID a happens-before event ID b.
func (s *System) HappensBefore(a, b int) bool { return s.h.HappensBefore(a, b) }

// SelfCheck verifies the state invariants on the current history.
func (s *System) SelfCheck() error { return s.h.SelfCheck() }

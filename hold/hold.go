// Package hold exposes dispute holds over event-time intervals.
package hold

import "ontology/internal/core"

var (
	ErrInvalidParam = core.ErrInvalidParam
	ErrClockBack    = core.ErrClockBack
	ErrNoSplit      = core.ErrNoSplit
	ErrHoldExists   = core.ErrHoldExists
	ErrHoldNotFound = core.ErrHoldNotFound
)

// Service handles Hold and Release against the shared engine.
type Service struct{ eng *core.Engine }

func New(eng *core.Engine) *Service { return &Service{eng: eng} }

// Hold freezes the remaining part of every share of the content whose
// event time falls in the half-open interval [from, to), including events
// earned later whose time still falls in the interval. Already paid-out
// parts are not affected.
func (s *Service) Hold(now int64, holdID, content string, from, to int64) error {
	return s.eng.Hold(now, holdID, content, from, to)
}

// Release lifts one hold; a share covered by several holds unfreezes only
// after all of them are released.
func (s *Service) Release(now int64, holdID string) error {
	return s.eng.Release(now, holdID)
}

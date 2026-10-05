// Package payout exposes balance queries, settlement and refunds.
package payout

import "ontology/internal/core"

var (
	ErrInvalidParam    = core.ErrInvalidParam
	ErrClockBack       = core.ErrClockBack
	ErrEventNotFound   = core.ErrEventNotFound
	ErrAlreadyRefunded = core.ErrAlreadyRefunded
	ErrCreatorNotFound = core.ErrCreatorNotFound
)

// Service handles Balance, Settle and Refund against the shared engine.
type Service struct{ eng *core.Engine }

func New(eng *core.Engine) *Service { return &Service{eng: eng} }

// Balance classifies the creator's remaining shares into held (covered by
// any hold), pending (unfrozen but immature) and available (unfrozen and
// mature), and also reports the outstanding debt.
func (s *Service) Balance(creator string, now int64) (held, pending, available, debt int64) {
	return s.eng.Balance(creator, now)
}

// Settle consumes available shares FIFO: first offsets the debt, then pays
// out the net amount when it reaches the minimum threshold. It returns the
// paid-out amount and the debt-offset amount.
func (s *Service) Settle(now int64, creator string) (payout, offset int64, err error) {
	return s.eng.Settle(now, creator)
}

// Refund removes the remaining part of every share of the event and turns
// the already paid or offset part (share - remaining) into creator debt.
func (s *Service) Refund(now int64, eventID string) error {
	return s.eng.Refund(now, eventID)
}

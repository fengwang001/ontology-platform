// Package revenue exposes split-table management and revenue events.
package revenue

import "ontology/internal/core"

// Part is one creator's basis points in a split table.
type Part = core.Part

// Share is one allocated share of an Earn event.
type Share = core.ShareInfo

var (
	ErrInvalidParam  = core.ErrInvalidParam
	ErrClockBack     = core.ErrClockBack
	ErrNoSplit       = core.ErrNoSplit
	ErrEventConflict = core.ErrEventConflict
)

// Service handles SetSplit and Earn against the shared engine.
type Service struct{ eng *core.Engine }

func New(eng *core.Engine) *Service { return &Service{eng: eng} }

// SetSplit installs the split table of a content; it only affects later
// Earn events.
func (s *Service) SetSplit(now int64, content string, parts []Part) error {
	return s.eng.SetSplit(now, content, parts)
}

// Earn records a revenue event and allocates shares per the current split
// table; the rounding remainder goes to the first part. Replaying an
// identical eventID is a no-op returning the original shares.
func (s *Service) Earn(now int64, eventID, content string, amount int64) ([]Share, error) {
	return s.eng.Earn(now, eventID, content, amount)
}

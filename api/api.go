// Package api is the public facade for the incremental SEMI JOIN view.
// It depends only on package semi.
package api

import "ontology/semi"

// Re-exported sentinel errors; the four failure kinds stay distinct and
// remain decidable with errors.Is.
var (
	ErrInvalidMaxLeft = semi.ErrInvalidMaxLeft
	ErrLeftIDExists   = semi.ErrLeftIDExists
	ErrLeftTableFull  = semi.ErrLeftTableFull
	ErrLeftNotFound   = semi.ErrLeftNotFound
	ErrRightUnderflow = semi.ErrRightUnderflow
)

// Join is the public SEMI JOIN handle.
type Join struct {
	s *semi.Semi
}

// New creates a SEMI JOIN that accepts at most maxLeft left rows.
func New(maxLeft int) (*Join, error) {
	s, err := semi.New(maxLeft)
	if err != nil {
		return nil, err
	}
	return &Join{s: s}, nil
}

func (j *Join) AddLeft(id int64, key *string) error { return j.s.AddLeft(id, key) }

func (j *Join) DelLeft(id int64) error { return j.s.DelLeft(id) }

func (j *Join) AddRight(key *string) error { return j.s.AddRight(key) }

func (j *Join) DelRight(key *string) error { return j.s.DelRight(key) }

// View returns retained left ids in ascending order.
func (j *Join) View() []int64 { return j.s.View() }

// SelfCheck runs the built-in invariant checks.
func (j *Join) SelfCheck() error { return j.s.SelfCheck() }

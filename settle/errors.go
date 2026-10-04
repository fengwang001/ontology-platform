package settle

import "errors"

var (
	ErrInvalid      = errors.New("settle: invalid argument")
	ErrClockBack    = errors.New("settle: clock moved backwards")
	ErrNoPerson     = errors.New("settle: person not found")
	ErrDupID        = errors.New("settle: duplicate settlement id")
	ErrNoItem       = errors.New("settle: catalog item not found")
	ErrYearClosed   = errors.New("settle: year already closed")
	ErrNoSettlement = errors.New("settle: settlement not found")
	ErrReversed     = errors.New("settle: settlement already reversed")
	ErrNotLast      = errors.New("settle: settlement is not the last active one")
)

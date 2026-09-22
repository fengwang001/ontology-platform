package reasm

import "errors"

// Distinguishable submission errors returned by Submit.
var (
	// ErrEmptyData: a fragment carried no payload bytes.
	ErrEmptyData = errors.New("reasm: empty fragment data")
	// ErrZeroTotal: the declared total message length is zero.
	ErrZeroTotal = errors.New("reasm: total length is zero")
	// ErrOutOfRange: offset+len(data) exceeds the declared total.
	ErrOutOfRange = errors.New("reasm: fragment out of range")
	// ErrTotalMismatch: total differs from the one already recorded
	// for this message ID.
	ErrTotalMismatch = errors.New("reasm: inconsistent total length")
	// ErrBudget: accepting the fragment would exceed the byte budget.
	ErrBudget = errors.New("reasm: byte budget exceeded")
)

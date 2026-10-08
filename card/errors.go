package card

import "errors"

// Errors returned by ledger operations. When several problems apply, only
// the first one in this priority order is reported:
//
//	ErrInvalidParam > ErrClockRollback > ErrAccountNotFound > ErrBillingTooEarly
//
// ErrAccountExists is only relevant for CreateAccount and ranks together
// with ErrAccountNotFound (after the clock check).
var (
	// ErrInvalidParam is returned for zero/negative amounts, unknown
	// categories, negative days, invalid account parameters or empty
	// account IDs.
	ErrInvalidParam = errors.New("card: invalid parameter")
	// ErrClockRollback is returned when now is smaller than the last
	// accepted operation's now.
	ErrClockRollback = errors.New("card: clock rollback")
	// ErrAccountNotFound is returned when the account ID does not exist.
	ErrAccountNotFound = errors.New("card: account not found")
	// ErrBillingTooEarly is returned when a billing day is not later
	// than the previous statement's due day.
	ErrBillingTooEarly = errors.New("card: billing too early")
	// ErrAccountExists is returned when creating an account whose ID
	// is already taken.
	ErrAccountExists = errors.New("card: account already exists")
)

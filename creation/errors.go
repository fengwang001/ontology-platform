package creation

import "errors"

var (
	ErrInvalidArgument  = errors.New("invalid argument")
	ErrClockRollback    = errors.New("clock rollback")
	ErrNotFound         = errors.New("not found")
	ErrDuplicateID      = errors.New("duplicate create id")
	ErrDailyLimit       = errors.New("daily creation unit limit exceeded")
	ErrInsufficientSec  = errors.New("insufficient component security")
	ErrRatioExceeded    = errors.New("cash substitution ratio exceeded")
	ErrInsufficientCash = errors.New("insufficient cash")
	ErrInsufficientUnit = errors.New("insufficient redeemable units")
	ErrInsufficientInv  = errors.New("insufficient fund inventory")
)

type itemError struct {
	cause error
	item  string
}

func (e itemError) Error() string { return e.cause.Error() + ": " + e.item }
func (e itemError) Unwrap() error { return e.cause }
func (e itemError) Item() string  { return e.item }

package settlement

import "errors"

var (
	ErrInvalidArgument = errors.New("settlement: invalid argument")
	ErrClockRolledBack = errors.New("settlement: clock rolled back")
	ErrMerchantMissing = errors.New("settlement: merchant not found")
	ErrDuplicateTxnID  = errors.New("settlement: duplicate transaction id")
	ErrInvalidDate     = errors.New("settlement: transaction date not before now")
	ErrBookSealed      = errors.New("settlement: book already sealed for that date")
	ErrNonBusinessDay  = errors.New("settlement: date is not a business day")
	ErrDuplicateSettle = errors.New("settlement: settlement already processed through that day")
)

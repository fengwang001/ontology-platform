package settlement

import "errors"

var (
	ErrInvalidParameter = errors.New("invalid parameter")
	ErrNonBusinessDay   = errors.New("non-business day")
	ErrOutOfOrder       = errors.New("batch processing is out of order")
	ErrDuplicateOrder   = errors.New("duplicate order id")
	ErrAccountNotFound  = errors.New("account not found")
	ErrDatePassed       = errors.New("settlement date has passed")
	ErrOrderNotFound    = errors.New("order not found")
)

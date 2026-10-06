package settlement

// errors.go —— 可区分的业务错误。

import "errors"

var (
	ErrInvalidParam   = errors.New("invalid parameter")
	ErrNonBusinessDay = errors.New("not a business day")
	ErrOrdering       = errors.New("batch ordering error")
	ErrDuplicateID    = errors.New("duplicate order id")
	ErrAccountMissing = errors.New("account does not exist")
	ErrDatePassed     = errors.New("settlement date already passed")
	ErrOrderNotFound  = errors.New("order not found")
)

package chargeback

import "errors"

var (
	ErrInvalidArgument     = errors.New("invalid argument")
	ErrClockRollback       = errors.New("clock rollback")
	ErrTransactionNotFound = errors.New("transaction not found")
	ErrWindowExpired       = errors.New("filing window expired")
	ErrDuplicateFiling     = errors.New("duplicate filing")
	ErrAmountExceeded      = errors.New("disputable amount exceeded")
	ErrNoBasis             = errors.New("no basis transaction for duplicate chargeback")
	ErrCaseNotFound        = errors.New("case not found")
	ErrStatusNotAllowed    = errors.New("operation not allowed in current status")
)

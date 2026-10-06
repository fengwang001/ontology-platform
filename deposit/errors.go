// Package deposit provides a rental security-deposit deduction, dispute
// and refund service.
package deposit

import "errors"

// ErrorCode identifies the reason a request was rejected.  Callers can use
// errors.Is with the sentinel values below; codes are also reported in the
// first-error ordering required by the specification.
type ErrorCode int

const (
	ErrUnknown ErrorCode = iota
	ErrInvalidParameter
	ErrClockBackward
	ErrLeaseNotFound
	ErrNotCheckedOut
	ErrDeclarationLate
	ErrDisputeLate
	ErrIllegalState
	ErrAmountOutOfRange
)

// Sentinel errors, ordered so that the service can always report only the
// first applicable rejection reason.
var (
	ErrInvalidParam  = errors.New("invalid parameter")
	ErrClockRollback = errors.New("clock moved backwards")
	ErrNoLease       = errors.New("lease not found")
	ErrNotOut        = errors.New("lease has not checked out")
	ErrLateDeclare   = errors.New("deduction declaration period expired")
	ErrLateDispute   = errors.New("dispute period expired")
	ErrState         = errors.New("operation not allowed in current state")
	ErrAmount        = errors.New("amount out of allowed range")
)

type serviceError struct {
	code ErrorCode
	err  error
}

func (e *serviceError) Error() string { return e.err.Error() }
func (e *serviceError) Unwrap() error { return e.err }

func reject(code ErrorCode) error {
	switch code {
	case ErrInvalidParameter:
		return &serviceError{code, ErrInvalidParam}
	case ErrClockBackward:
		return &serviceError{code, ErrClockRollback}
	case ErrLeaseNotFound:
		return &serviceError{code, ErrNoLease}
	case ErrNotCheckedOut:
		return &serviceError{code, ErrNotOut}
	case ErrDeclarationLate:
		return &serviceError{code, ErrLateDeclare}
	case ErrDisputeLate:
		return &serviceError{code, ErrLateDispute}
	case ErrIllegalState:
		return &serviceError{code, ErrState}
	case ErrAmountOutOfRange:
		return &serviceError{code, ErrAmount}
	}
	return &serviceError{ErrUnknown, errors.New("unknown error")}
}

// CodeOf returns the ErrorCode of an error returned by the service.
func CodeOf(err error) ErrorCode {
	var se *serviceError
	if errors.As(err, &se) {
		return se.code
	}
	return ErrUnknown
}

package fence

import "fmt"

// ErrorCode lets callers programmatically distinguish every rejection class.
// The order below also mirrors the required rejection precedence; each
// operation reports only the first matching error.
type ErrorCode int

const (
	OK ErrorCode = iota
	ErrInvalidArgument
	ErrClockRollback
	ErrMerchantNotFound
	ErrOrderNotFound
	ErrEventNotFound
	ErrOrderAccepted
	ErrOrderDelivered
	ErrOrderCancelled
	ErrOrderRerouted
	ErrEventTerminated
	ErrOrderNotAccepted
	ErrOutsideForever
	ErrTemporarilyUnreachable
)

// FenceError is the only error type returned by accepted-validation paths.
type FenceError struct {
	Code ErrorCode
	Msg  string
}

func (e *FenceError) Error() string { return e.Msg }

func fail(code ErrorCode, format string, args ...any) error {
	return &FenceError{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// Code extracts the ErrorCode from an error, or OK for nil.
func Code(err error) ErrorCode {
	if err == nil {
		return OK
	}
	if fe, ok := err.(*FenceError); ok {
		return fe.Code
	}
	return ErrInvalidArgument
}

package railway

import "fmt"

// ErrorCode values follow the required rejection precedence.
type ErrorCode int

const (
	ErrInvalidArgument ErrorCode = iota + 1
	ErrClockRewind
	ErrTrainNotFound
	ErrTicketNotFound
	ErrTicketRefunded
	ErrDeparted
	ErrPassengerOverlap
	ErrQuotaExhausted
	ErrNoSeatAvailable
)

type RailwayError struct {
	Code ErrorCode
	msg  string
}

func (e RailwayError) Error() string {
	return e.msg
}

func railwayError(code ErrorCode, format string, args ...any) error {
	return RailwayError{Code: code, msg: fmt.Sprintf(format, args...)}
}

// ErrorCodeOf returns zero for nil, otherwise the canonical railway error code.
func ErrorCodeOf(err error) ErrorCode {
	if err == nil {
		return 0
	}
	if e, ok := err.(RailwayError); ok {
		return e.Code
	}
	return ErrInvalidArgument
}

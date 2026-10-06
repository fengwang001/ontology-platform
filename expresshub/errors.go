package expresshub

import "fmt"

type ErrorCode string

const (
	InvalidArgument ErrorCode = "invalid_argument"
	ClockRewound    ErrorCode = "clock_rewound"
	NotFound        ErrorCode = "not_found"
	InvalidState    ErrorCode = "invalid_state"
	StationMismatch ErrorCode = "station_mismatch"
	BusinessReject  ErrorCode = "business_reject"
)

type Error struct {
	Code ErrorCode
	Msg  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Msg)
}

func errorf(code ErrorCode, format string, args ...any) error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

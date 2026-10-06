package lease

type ErrorCode string

const (
	InvalidArgument ErrorCode = "invalid_argument"
	ClockRollback   ErrorCode = "clock_rollback"
	LeaseNotFound   ErrorCode = "lease_not_found"
	InvalidState    ErrorCode = "invalid_state"
	RentAboveCap    ErrorCode = "rent_above_cap"
	LateResponse    ErrorCode = "late_response"
)

type Error struct {
	Code   ErrorCode
	Reason string
}

func (e Error) Error() string {
	return string(e.Code) + ": " + e.Reason
}

func invalidArgument(reason string) error {
	return Error{Code: InvalidArgument, Reason: reason}
}

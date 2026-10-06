package ontology

type ErrorCode string

const (
	ErrIllegalArgument     ErrorCode = "illegal_argument"
	ErrClockRewind         ErrorCode = "clock_rewind"
	ErrNotFound            ErrorCode = "not_found"
	ErrTimeWindow          ErrorCode = "time_window"
	ErrInvalidState        ErrorCode = "invalid_state"
	ErrQuietConflict       ErrorCode = "quiet_conflict"
	ErrInsufficientDeposit ErrorCode = "insufficient_deposit"
)

type Error struct {
	Code   ErrorCode
	Reason string
}

func (e Error) Error() string {
	return string(e.Code) + ": " + e.Reason
}

func illegal(why string) error    { return Error{ErrIllegalArgument, why} }
func notFound(why string) error   { return Error{ErrNotFound, why} }
func timeWindow(why string) error { return Error{ErrTimeWindow, why} }
func badState(why string) error   { return Error{ErrInvalidState, why} }
func quiet(why string) error      { return Error{ErrQuietConflict, why} }
func noMoney(why string) error    { return Error{ErrInsufficientDeposit, why} }

package ontology

type ErrorCode string

const (
	ErrInvalidParameter ErrorCode = "invalid_parameter"
	ErrClockRollback    ErrorCode = "clock_rollback"
	ErrNotFound         ErrorCode = "not_found"
	ErrInvalidState     ErrorCode = "invalid_state"
	ErrForcedClosure    ErrorCode = "forced_closure"
	ErrTemporaryClosure ErrorCode = "temporary_closure"
	ErrClosureGap       ErrorCode = "closure_gap"
	ErrClosureOverlap   ErrorCode = "closure_overlap"
	ErrOpenOrders       ErrorCode = "open_orders"
	ErrNotOpen          ErrorCode = "not_open"
	ErrNearClosing      ErrorCode = "near_closing"
	ErrReservationFar   ErrorCode = "reservation_too_far"
)

type CallError struct {
	Code    ErrorCode
	Message string
}

func (e *CallError) Error() string {
	return string(e.Code) + ": " + e.Message
}

func errorCode(err error) ErrorCode {
	return ""
}

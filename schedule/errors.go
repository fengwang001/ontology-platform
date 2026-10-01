package schedule

// ErrorCode 以稳定字符串标识每一类可区分的拒绝原因。
type ErrorCode string

const (
	ErrInvalidDate      ErrorCode = "invalid_date"
	ErrInvalidInterval  ErrorCode = "invalid_interval"
	ErrInvalidNth       ErrorCode = "invalid_nth"
	ErrInvalidWeekday   ErrorCode = "invalid_weekday"
	ErrInvalidEnd       ErrorCode = "invalid_end"
	ErrUntilBeforeStart ErrorCode = "until_before_start"
	ErrDuplicateSeries  ErrorCode = "duplicate_series_id"
	ErrUnknownSeries    ErrorCode = "unknown_series_id"
	ErrNotInstance      ErrorCode = "not_instance_date"
	ErrAlreadyCanceled  ErrorCode = "already_canceled"
	ErrDateOccupied     ErrorCode = "reschedule_date_occupied"
	ErrInvalidRange     ErrorCode = "invalid_expand_range"
	ErrInvalidNewRule   ErrorCode = "invalid_new_rule"
)

// OpError 携带可区分的错误码与可读说明，被拒绝的操作不会改动任何系列。
type OpError struct {
	Code   ErrorCode
	Reason string
}

func (e *OpError) Error() string {
	return string(e.Code) + ": " + e.Reason
}

func opError(code ErrorCode, reason string) *OpError {
	return &OpError{Code: code, Reason: reason}
}

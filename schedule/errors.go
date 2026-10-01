package schedule

import "errors"

// 所有可被调用方用 errors.Is 区分的拒绝原因。
var (
	ErrDateFormat       = errors.New("schedule: malformed date, want YYYY-MM-DD")
	ErrDateInvalid      = errors.New("schedule: date does not exist in the Gregorian calendar")
	ErrDateRange        = errors.New("schedule: date outside [1970-01-01, 2200-12-31]")
	ErrInvalidInterval  = errors.New("schedule: interval months must be >= 1")
	ErrInvalidNth       = errors.New("schedule: nth must be -1 (last) or between 1 and 5")
	ErrInvalidWeekday   = errors.New("schedule: weekday must be 1 (Mon) .. 7 (Sun)")
	ErrInvalidCount     = errors.New("schedule: count must be >= 1")
	ErrTermination      = errors.New("schedule: exactly one of count / until must be given")
	ErrUntilBeforeStart = errors.New("schedule: until is before start")
	ErrSeriesExists     = errors.New("schedule: series id already exists")
	ErrSeriesNotFound   = errors.New("schedule: series id not found")
	ErrNotInstanceDate  = errors.New("schedule: date is not an instance original date of the series")
	ErrAlreadyCanceled  = errors.New("schedule: instance already canceled")
	ErrDateOccupied     = errors.New("schedule: target date occupied by another instance of the same series")
	ErrInvalidRange     = errors.New("schedule: expand range is empty or longer than 3660 days")
)

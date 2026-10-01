package schedule

import "fmt"

const (
	minYear = 1970
	maxYear = 2200
)

// ErrCode 以可区分的字符串标识每个操作被整体拒绝的原因。
type ErrCode string

const (
	// ErrDateFormat 日期文本不是 YYYY-MM-DD 形式。
	ErrDateFormat ErrCode = "INVALID_DATE_FORMAT"
	// ErrDateRange 日期越出 1970-01-01..2200-12-31，或公历日期不存在（如 2 月 30 日）。
	ErrDateRange ErrCode = "INVALID_DATE"
	// ErrBadInterval 间隔月数 k 小于 1。
	ErrBadInterval ErrCode = "INVALID_INTERVAL"
	// ErrBadNth nth 为 0、小于 -1 或大于 5。
	ErrBadNth ErrCode = "INVALID_NTH"
	// ErrBadWeekday 星期 w 不在 1..7。
	ErrBadWeekday ErrCode = "INVALID_WEEKDAY"
	// ErrTerminalCountUntil count 与 until 同时给出或都不给出。
	ErrTerminalCountUntil ErrCode = "INVALID_TERMINATION"
	// ErrCountRange count 小于 1。
	ErrCountRange ErrCode = "INVALID_COUNT"
	// ErrUntilBeforeStart until 早于 start（创建时）。
	ErrUntilBeforeStart ErrCode = "UNTIL_BEFORE_START"
	// ErrDuplicateID 系列 id 已存在。
	ErrDuplicateID ErrCode = "DUPLICATE_SERIES_ID"
	// ErrUnknownSeries 系列 id 不存在。
	ErrUnknownSeries ErrCode = "UNKNOWN_SERIES"
	// ErrNotInstance 给定日期不是该系列的实例原日期。
	ErrNotInstance ErrCode = "NOT_AN_INSTANCE"
	// ErrAlreadyCancelled 取消一个已取消的实例，或改期一个已取消的实例。
	ErrAlreadyCancelled ErrCode = "INSTANCE_ALREADY_CANCELLED"
	// ErrTargetOccupied 改期目标日已被同系列另一实例（原位或改期而来）占用。
	ErrTargetOccupied ErrCode = "TARGET_DATE_OCCUPIED"
	// ErrEmptyRange 展开区间为空（from >= to）。
	ErrEmptyRange ErrCode = "EMPTY_RANGE"
	// ErrRangeTooWide 展开区间跨度超过 3660 天。
	ErrRangeTooWide ErrCode = "RANGE_TOO_WIDE"
)

// Error 携带可区分原因码与描述，调用方可通过 *Error 的 Code 精确判定。
type Error struct {
	Code ErrCode
	Msg  string
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Msg }

func errf(code ErrCode, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// Date 是经过校验的公历日期，内部为儒略日序号（从负无穷连续编号的整数天）。
type Date struct {
	ordinal int
}

// Rule 描述“每 k 个月的第 nth 个星期 w”。
type Rule struct {
	IntervalMonths int
	Nth            int
	Weekday        int
}

// Termination 恰有一个字段有效：Count>0 表示总实例数，Until 非空表示截止日（含当日）。
type Termination struct {
	Count int
	Until string
}

// CreateInput 是创建系列的入参。
type CreateInput struct {
	ID    string
	Start string
	Rule  Rule
	Term  Termination
}

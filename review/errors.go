package review

import "fmt"

// ErrorKind 错误类别。声明顺序即优先级：同一操作违反多条规则时，
// 只报告优先级最高（声明最靠前）的第一个错误。
type ErrorKind int

const (
	// ErrInvalidParam 参数非法（如人数为偶数、选项非法、轮次越界）。
	ErrInvalidParam ErrorKind = iota
	// ErrClockRollback 时钟回退（操作时间早于服务当前逻辑时钟）。
	ErrClockRollback
	// ErrNotFound 申报人、评委或评审不存在。
	ErrNotFound
	// ErrStateNotAllowed 当前状态不允许该操作（如重复登记、评审已结束）。
	ErrStateNotAllowed
	// ErrNoPermission 无权限（投票者不是本评审现任评委）。
	ErrNoPermission
	// ErrInsufficientExperts 满足条件的评委不足，无法成组。
	ErrInsufficientExperts
	// ErrDuplicateVote 同一轮内重复投票。
	ErrDuplicateVote
)

func (k ErrorKind) String() string {
	switch k {
	case ErrInvalidParam:
		return "invalid_param"
	case ErrClockRollback:
		return "clock_rollback"
	case ErrNotFound:
		return "not_found"
	case ErrStateNotAllowed:
		return "state_not_allowed"
	case ErrNoPermission:
		return "no_permission"
	case ErrInsufficientExperts:
		return "insufficient_experts"
	case ErrDuplicateVote:
		return "duplicate_vote"
	}
	return "unknown"
}

// Error 是可区分的操作错误。被拒绝的操作不改变任何状态与时钟。
type Error struct {
	Kind   ErrorKind
	Op     string
	Detail string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s: %s", e.Op, e.Kind, e.Detail)
}

func newErr(kind ErrorKind, op, format string, args ...any) *Error {
	return &Error{Kind: kind, Op: op, Detail: fmt.Sprintf(format, args...)}
}

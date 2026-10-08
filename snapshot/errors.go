package snapshot

import "fmt"

// Kind 标识可区分的错误类别。声明顺序即全局拒绝优先次序：
// 参数非法、时钟回退、组或卷不存在、组冲突、状态错误、重复确认、队列已满。
// 一次操作至多报告次序最靠前的一类错误。
type Kind int

const (
	KindInvalidArgument Kind = iota
	KindClockRegression
	KindNotFound
	KindGroupConflict
	KindStateError
	KindDuplicateConfirm
	KindQueueFull
)

var kindNames = map[Kind]string{
	KindInvalidArgument:  "invalid argument",
	KindClockRegression:  "clock regression",
	KindNotFound:         "not found",
	KindGroupConflict:    "group conflict",
	KindStateError:       "state error",
	KindDuplicateConfirm: "duplicate confirm",
	KindQueueFull:        "queue full",
}

func (k Kind) String() string {
	if name, ok := kindNames[k]; ok {
		return name
	}
	return fmt.Sprintf("kind(%d)", int(k))
}

// Error 是协调服务返回的唯一错误类型，调用方可凭 Kind 区分错误类别。
type Error struct {
	Kind    Kind
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Kind, e.Message)
}

func invalidArgf(format string, args ...any) *Error {
	return &Error{Kind: KindInvalidArgument, Message: fmt.Sprintf(format, args...)}
}

func clockRegressionf(format string, args ...any) *Error {
	return &Error{Kind: KindClockRegression, Message: fmt.Sprintf(format, args...)}
}

func notFoundf(format string, args ...any) *Error {
	return &Error{Kind: KindNotFound, Message: fmt.Sprintf(format, args...)}
}

func groupConflictf(format string, args ...any) *Error {
	return &Error{Kind: KindGroupConflict, Message: fmt.Sprintf(format, args...)}
}

func stateErrorf(format string, args ...any) *Error {
	return &Error{Kind: KindStateError, Message: fmt.Sprintf(format, args...)}
}

func duplicateConfirmf(format string, args ...any) *Error {
	return &Error{Kind: KindDuplicateConfirm, Message: fmt.Sprintf(format, args...)}
}

func queueFullf(format string, args ...any) *Error {
	return &Error{Kind: KindQueueFull, Message: fmt.Sprintf(format, args...)}
}

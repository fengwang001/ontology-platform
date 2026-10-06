package mirror

import "fmt"

// ErrKind 标识可区分的错误类别。声明顺序即上报优先级：
// 一次调用同时满足多类错误时，只报次序最靠前的一类。
type ErrKind int

const (
	ErrInvalidArgument   ErrKind = iota // 参数非法
	ErrNoSuchMember                     // 成员不存在
	ErrInvalidState                     // 状态不符
	ErrGenerationAhead                  // 世代超前
	ErrNotAuthoritative                 // 非权威成员
	ErrVolumeUnavailable                // 卷不可用
)

func (k ErrKind) String() string {
	switch k {
	case ErrInvalidArgument:
		return "参数非法"
	case ErrNoSuchMember:
		return "成员不存在"
	case ErrInvalidState:
		return "状态不符"
	case ErrGenerationAhead:
		return "世代超前"
	case ErrNotAuthoritative:
		return "非权威成员"
	case ErrVolumeUnavailable:
		return "卷不可用"
	}
	return "未知错误"
}

// Error 是卷服务返回的唯一错误类型，Kind 可被调用方精确判定。
type Error struct {
	Kind ErrKind
	Msg  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Kind, e.Msg)
}

// KindOf 提取错误的类别；ok 为 false 表示不是本服务产生的错误。
func KindOf(err error) (kind ErrKind, ok bool) {
	if e, isErr := err.(*Error); isErr {
		return e.Kind, true
	}
	return 0, false
}

func errf(kind ErrKind, format string, args ...any) *Error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}

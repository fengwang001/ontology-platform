package mirror

import "fmt"

// ErrKind 标识可区分的错误类别。声明顺序即上报优先级：
// 一次调用若同时命中多类错误，只报次序最靠前的一类。
type ErrKind int

const (
	// ErrInvalidArg 参数非法（块号越界、批量上限非正、成员数/块数/脏区上限配置非法等）。
	ErrInvalidArg ErrKind = iota
	// ErrNoSuchMember 成员不存在（成员编号越界）。
	ErrNoSuchMember
	// ErrBadState 状态不符（如对故障成员再次报告故障、对已在线成员重新加入等）。
	ErrBadState
	// ErrGenerationAhead 世代超前（盘面世代标签大于卷当前世代）。
	ErrGenerationAhead
	// ErrNotAuthoritative 非权威成员（全部故障后首个重新加入者不是故障世代最大者）。
	ErrNotAuthoritative
	// ErrVolumeUnavailable 卷不可用（无在线成员，或一次写入中全部在线成员都写失败）。
	ErrVolumeUnavailable
)

// Error 是卷服务返回的唯一错误类型，调用方用 Kind 区分类别。
type Error struct {
	Kind   ErrKind
	Detail string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Kind, e.Detail)
}

func (k ErrKind) String() string {
	switch k {
	case ErrInvalidArg:
		return "参数非法"
	case ErrNoSuchMember:
		return "成员不存在"
	case ErrBadState:
		return "状态不符"
	case ErrGenerationAhead:
		return "世代超前"
	case ErrNotAuthoritative:
		return "非权威成员"
	case ErrVolumeUnavailable:
		return "卷不可用"
	default:
		return "未知错误"
	}
}

// KindOf 从 error 中取出 ErrKind，供调用方与测试做分类断言。
func KindOf(err error) (ErrKind, bool) {
	if e, ok := err.(*Error); ok {
		return e.Kind, true
	}
	return 0, false
}

func newErr(kind ErrKind, format string, args ...interface{}) *Error {
	return &Error{Kind: kind, Detail: fmt.Sprintf(format, args...)}
}

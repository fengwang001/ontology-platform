package idb

import "fmt"

// ErrKind 把所有可区分的拒绝原因编码为单一枚举。
// 拒绝次序（参数非法 → 版本过低 → 状态不允许 → 仓库不存在 →
// 作用域不符 → 事务已结束 → 键冲突）由各入口按此顺序显式检查保证。
type ErrKind int

const (
	KindOK              ErrKind = iota
	KindInvalidArgument         // 参数非法：空名字、非正版本、空作用域等
	KindVersionTooLow           // 版本过低
	KindInvalidState            // 状态不允许
	KindNoObjectStore           // 仓库不存在
	KindScopeViolation          // 作用域不符
	KindTxInactive              // 事务已结束
	KindConstraint              // 键冲突
	KindCanceled                // 被取消
)

// Error 携带类别与可读信息，调用方可按 Kind 精确区分八类拒绝。
type Error struct {
	Kind ErrKind
	Msg  string
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	return e.Kind.String() + ": " + e.Msg
}

func (k ErrKind) String() string {
	switch k {
	case KindInvalidArgument:
		return "InvalidArgument"
	case KindVersionTooLow:
		return "VersionTooLow"
	case KindInvalidState:
		return "InvalidState"
	case KindNoObjectStore:
		return "NoObjectStore"
	case KindScopeViolation:
		return "ScopeViolation"
	case KindTxInactive:
		return "TransactionInactive"
	case KindConstraint:
		return "ConstraintError"
	case KindCanceled:
		return "Canceled"
	default:
		return "OK"
	}
}

func newError(kind ErrKind, format string, args ...any) *Error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}

// New 以该类别构造一个错误（便于测试与调用方快速生成）。
func (k ErrKind) New(msg string) *Error {
	return &Error{Kind: k, Msg: msg}
}

func isKind(err error, kind ErrKind) bool {
	e, ok := err.(*Error)
	return ok && e.Kind == kind
}

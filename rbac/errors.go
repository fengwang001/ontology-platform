package rbac

import "fmt"

// ErrKind 区分拒绝类别，判定顺序为：参数非法 < 不存在 < 冲突 < 约束违反 < 超限。
type ErrKind int

const (
	KindInvalid   ErrKind = iota // 参数非法
	KindNotFound                 // 不存在
	KindConflict                 // 冲突
	KindViolation                // 约束违反
	KindLimit                    // 超限
)

func (k ErrKind) String() string {
	switch k {
	case KindInvalid:
		return "invalid"
	case KindNotFound:
		return "not-found"
	case KindConflict:
		return "conflict"
	case KindViolation:
		return "violation"
	case KindLimit:
		return "limit"
	}
	return "unknown"
}

// Error 是唯一导出的错误类型，带出定位信息。
type Error struct {
	Kind       ErrKind  // 拒绝类别
	Code       string   // 机器可读的细分原因
	Constraint string   // 约束违反时：约束名（字节序最小者）
	Object     string   // 约束违反时：用户/会话名（该约束下字节序最小者）
	Impliers   []string // 隐式激活时：蕴含它的显式角色列表（升序）
	Msg        string   // 人类可读描述
}

func (e *Error) Error() string { return e.Msg }

func errInvalid(code, format string, args ...interface{}) *Error {
	return &Error{Kind: KindInvalid, Code: code, Msg: fmt.Sprintf(format, args...)}
}

func errNotFound(code, format string, args ...interface{}) *Error {
	return &Error{Kind: KindNotFound, Code: code, Msg: fmt.Sprintf(format, args...)}
}

func errConflict(code, format string, args ...interface{}) *Error {
	return &Error{Kind: KindConflict, Code: code, Msg: fmt.Sprintf(format, args...)}
}

func errImplicitActive(role string, impliers []string) *Error {
	return &Error{
		Kind:     KindConflict,
		Code:     "implicit-active",
		Impliers: impliers,
		Msg:      fmt.Sprintf("role %q is only implicitly activated by %v", role, impliers),
	}
}

func errViolation(code, constraint, object string) *Error {
	return &Error{
		Kind:       KindViolation,
		Code:       code,
		Constraint: constraint,
		Object:     object,
		Msg:        fmt.Sprintf("%s: constraint %q violated by %q", code, constraint, object),
	}
}

func errLimit(code, format string, args ...interface{}) *Error {
	return &Error{Kind: KindLimit, Code: code, Msg: fmt.Sprintf(format, args...)}
}

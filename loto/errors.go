package loto

import "fmt"

// ErrKind 错误类别，按声明顺序即判定优先级：
// 参数非法 > 时刻回退 > 对象不存在 > 无权限或角色不符 > 状态不允许 > 冲突 > 条件不满足。
// 每个操作严格按此顺序逐项校验，返回首个命中的错误。
type ErrKind int

const (
	ErrInvalidParam   ErrKind = iota // 参数非法
	ErrTimeRegression                // 时刻回退
	ErrNotFound                      // 对象不存在
	ErrPermission                    // 无权限或角色不符
	ErrState                         // 状态不允许
	ErrConflict                      // 冲突
	ErrPrecondition                  // 条件不满足
)

func (k ErrKind) String() string {
	switch k {
	case ErrInvalidParam:
		return "参数非法"
	case ErrTimeRegression:
		return "时刻回退"
	case ErrNotFound:
		return "对象不存在"
	case ErrPermission:
		return "无权限或角色不符"
	case ErrState:
		return "状态不允许"
	case ErrConflict:
		return "冲突"
	case ErrPrecondition:
		return "条件不满足"
	}
	return "未知错误"
}

// Error 是系统所有被拒绝操作返回的错误类型。
type Error struct {
	Kind ErrKind
	Op   string
	Msg  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s: %s", e.Op, e.Kind, e.Msg)
}

func newErr(op string, kind ErrKind, format string, args ...any) *Error {
	return &Error{Kind: kind, Op: op, Msg: fmt.Sprintf(format, args...)}
}

// KindOf 从 error 中提取 ErrKind；非本系统错误返回 false。
func KindOf(err error) (ErrKind, bool) {
	if e, ok := err.(*Error); ok {
		return e.Kind, true
	}
	return 0, false
}

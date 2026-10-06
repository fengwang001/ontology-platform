package gc

import "fmt"

// ErrorKind 是可区分的错误类别。
//
// 优先级从高到低（数值越小优先级越高）：
// 参数非法 > 对象不存在 > 状态冲突 > 环 > 属主缺失。
// 一次操作违反多条规则时，报告优先级最高的错误，且不改变任何状态。
type ErrorKind int

const (
	// KindInvalidArgument 参数非法：空标识、非法策略、空终结器名、重复属主等。
	KindInvalidArgument ErrorKind = iota + 1
	// KindNotFound 对象不存在（含移除不存在的终结器）。
	KindNotFound
	// KindConflict 状态冲突：对象处于删除中时不允许的操作、重复创建等。
	KindConflict
	// KindCycle 属主关系成环（含自引用，自引用视为长度为 1 的环）。
	KindCycle
	// KindOwnerMissing 属主缺失：引用了不存在的属主对象。
	KindOwnerMissing
)

// Error 是控制器返回的错误，携带类别与可读信息。
type Error struct {
	Kind ErrorKind
	Msg  string
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Kind, e.Msg) }

func (k ErrorKind) String() string {
	switch k {
	case KindInvalidArgument:
		return "invalid argument"
	case KindNotFound:
		return "not found"
	case KindConflict:
		return "conflict"
	case KindCycle:
		return "cycle"
	case KindOwnerMissing:
		return "owner missing"
	default:
		return "unknown"
	}
}

func newError(kind ErrorKind, format string, args ...any) *Error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}

package subtype

import "errors"

// ErrorKind 区分登记与判定过程中可能出现的错误类别。
type ErrorKind int

const (
	// KindInvalidArgument 参数非法：空名字、类型表达式为 nil、
	// 同一对象内属性名重复等。
	KindInvalidArgument ErrorKind = iota
	// KindDuplicateDefinition 重复定义：同名命名类型已登记。
	KindDuplicateDefinition
	// KindUndefinedReference 未定义引用：待比较类型静态可达的命名类型未登记。
	KindUndefinedReference
	// KindUnguardedCycle 无保护循环：某命名类型不经过对象或函数构造子
	// 而展开回自身（包括经联合的间接回环）。
	KindUnguardedCycle
)

// 各类错误的哨兵值，可用 errors.Is 判断。
var (
	ErrInvalidArgument     = errors.New("subtype: invalid argument")
	ErrDuplicateDefinition = errors.New("subtype: duplicate definition")
	ErrUndefinedReference  = errors.New("subtype: undefined reference")
	ErrUnguardedCycle      = errors.New("subtype: unguarded cycle")
)

// Error 是登记与判定返回的错误，携带类别与相关的命名类型名。
type Error struct {
	Kind ErrorKind
	// Name 是相关的命名类型名：未定义引用与无保护循环时
	// 为字典序最小的相关名字，重复定义时为重复的名字。
	Name string
	// Detail 可选的补充说明。
	Detail string
}

func (e *Error) Error() string {
	msg := e.Kind.String()
	if e.Name != "" {
		msg += ": " + e.Name
	}
	if e.Detail != "" {
		msg += " (" + e.Detail + ")"
	}
	return "subtype: " + msg
}

// Is 使 errors.Is(err, ErrInvalidArgument) 等形式可用。
func (e *Error) Is(target error) bool {
	switch target {
	case ErrInvalidArgument:
		return e.Kind == KindInvalidArgument
	case ErrDuplicateDefinition:
		return e.Kind == KindDuplicateDefinition
	case ErrUndefinedReference:
		return e.Kind == KindUndefinedReference
	case ErrUnguardedCycle:
		return e.Kind == KindUnguardedCycle
	}
	return false
}

func (k ErrorKind) String() string {
	switch k {
	case KindInvalidArgument:
		return "invalid argument"
	case KindDuplicateDefinition:
		return "duplicate definition"
	case KindUndefinedReference:
		return "undefined reference"
	case KindUnguardedCycle:
		return "unguarded cycle"
	}
	return "unknown error"
}

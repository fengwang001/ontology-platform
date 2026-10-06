package subtype

import "errors"

// ErrorKind 区分登记与判定可能返回的错误类别。
type ErrorKind int

const (
	// ErrInvalidArgument 参数非法：空名字、对象属性名重复、类型表达式结构残缺等。
	ErrInvalidArgument ErrorKind = iota
	// ErrDuplicateDefinition 重复登记同名类型。
	ErrDuplicateDefinition
	// ErrUndefinedReference 待比较类型静态可达的命名定义中存在未登记的名字。
	ErrUndefinedReference
	// ErrUnguardedCycle 某命名类型不经过对象/函数构造子而展开回自身。
	ErrUnguardedCycle
)

// Error 是登记与判定返回的错误，携带类别与相关的命名类型名。
type Error struct {
	Kind   ErrorKind
	Name   string
	Detail string
}

func (e *Error) Error() string {
	switch e.Kind {
	case ErrInvalidArgument:
		return "subtype: invalid argument: " + e.Detail
	case ErrDuplicateDefinition:
		return "subtype: duplicate definition of " + quote(e.Name)
	case ErrUndefinedReference:
		return "subtype: undefined reference to " + quote(e.Name)
	case ErrUnguardedCycle:
		return "subtype: unguarded cycle through " + quote(e.Name)
	}
	return "subtype: unknown error"
}

// HasKind 报告 err 是否为指定类别的错误。
func HasKind(err error, k ErrorKind) bool {
	var se *Error
	if errors.As(err, &se) {
		return se.Kind == k
	}
	return false
}

// ErrorName 取出错误携带的命名类型名（无则返回空串）。
func ErrorName(err error) string {
	var se *Error
	if errors.As(err, &se) {
		return se.Name
	}
	return ""
}

func quote(s string) string { return `"` + s + `"` }

func invalidf(detail string) *Error {
	return &Error{Kind: ErrInvalidArgument, Detail: detail}
}

// Package exports implements module package exports-map resolution.
//
// 包内职责划分：
//   - errors.go   错误类别与错误类型
//   - target.go   目标（Target）数据模型
//   - index.go    子路径查找索引（精确键 + 通配键），查找开销与表规模无关
//   - table.go    导出映射表的构造、校验与纯函数式解析
//   - resolver.go 并发安全的解析器，支持整表原子替换
package exports

import (
	"errors"
	"fmt"
)

// ErrorKind 区分各类解析/构造错误。
type ErrorKind int

const (
	// KindInvalidRequest 请求非法：子路径不是 "." 或不以 "./" 开头、含 "*"，或条件名为空串。
	KindInvalidRequest ErrorKind = iota
	// KindInvalidTable 表非法：只会在构造或替换时出现。
	KindInvalidTable
	// KindNotExported 子路径未导出：没有任何键命中。
	KindNotExported
	// KindForbidden 被禁止：命中显式禁止目标。
	KindForbidden
	// KindNoMatchingCondition 无匹配条件：条件映射中没有任何条件命中。
	KindNoMatchingCondition
	// KindInvalidTarget 非法目标：字符串目标替换后不合法。
	KindInvalidTarget
)

func (k ErrorKind) String() string {
	switch k {
	case KindInvalidRequest:
		return "invalid request"
	case KindInvalidTable:
		return "invalid table"
	case KindNotExported:
		return "subpath not exported"
	case KindForbidden:
		return "forbidden"
	case KindNoMatchingCondition:
		return "no matching condition"
	case KindInvalidTarget:
		return "invalid target"
	default:
		return fmt.Sprintf("unknown error kind %d", int(k))
	}
}

// Error 是包内所有错误的统一类型，通过 Kind 字段区分错误类别。
type Error struct {
	Kind ErrorKind
	Msg  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("exports: %s: %s", e.Kind, e.Msg)
}

// IsKind 报告 err 是否为类别 k 的错误。
func IsKind(err error, k ErrorKind) bool {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind == k
	}
	return false
}

func newError(k ErrorKind, format string, args ...any) *Error {
	return &Error{Kind: k, Msg: fmt.Sprintf(format, args...)}
}

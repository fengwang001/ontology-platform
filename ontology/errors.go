// Package ontology 提供在表结构演进下按列稳定标识解码历史版本事件的组件。
//
// 核心设计：
//   - 每列有一经分配永不复用的稳定标识 ColumnID；
//   - 演进（末尾新增 / 删除 / 改名）只追加不可变的版本快照；
//   - 解码按列标识取值，事件中缺失的列取当前结构默认值；
//   - 所有被拒绝的操作都在修改状态之前完成校验，因此不会改变版本列表或标识分配。
package ontology

import (
	"fmt"
	"strconv"
)

// ErrorKind 是拒绝操作的可区分原因分类。
type ErrorKind string

// 所有对外暴露的拒绝原因。判定方可用 errors.Is 与对应的哨兵错误比较，
// 也可直接读取 Error.Kind。
const (
	// KindInvalidSchema: 初始化结构非法（空列、重名等）。
	KindInvalidSchema ErrorKind = "invalid_schema"
	// KindInvalidEvolution: 演进操作非法（操作已删除/不存在的列、列名冲突等）。
	KindInvalidEvolution ErrorKind = "invalid_evolution"
	// KindVersionNotFound: 事件所声称的版本不存在。
	KindVersionNotFound ErrorKind = "version_not_found"
	// KindValueCountMismatch: 事件值个数与所在版本的列数不符。
	KindValueCountMismatch ErrorKind = "value_count_mismatch"
	// KindVersionLimitExceeded: 版本数超过配置上限。
	KindVersionLimitExceeded ErrorKind = "version_limit_exceeded"
)

// Error 携带结构化的拒绝原因，便于调用方区分处理。
type Error struct {
	// Kind 是错误类别。
	Kind ErrorKind
	// Op 是被拒绝的操作名（如 "add_column"、"decode"）。
	Op string
	// Version 是相关版本号（若适用），否则为 0。
	Version int
	// Message 是人可读的细节说明。
	Message string
}

// Error 实现 error 接口。
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	prefix := string(e.Kind)
	if e.Op != "" {
		prefix += " [" + e.Op + "]"
	}
	if e.Version > 0 {
		prefix += " version=" + strconv.Itoa(e.Version)
	}
	if e.Message != "" {
		return prefix + ": " + e.Message
	}
	return prefix
}

// Is 使 errors.Is 按 Kind 匹配对应的哨兵错误：
// 例如任何 Kind 为 KindVersionNotFound 的 *Error 都满足
// errors.Is(err, ErrVersionNotFound)。
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	if !ok {
		return false
	}
	return e.Kind == t.Kind
}

// 哨兵错误，供 errors.Is 使用。
var (
	ErrInvalidSchema        = &Error{Kind: KindInvalidSchema}
	ErrInvalidEvolution     = &Error{Kind: KindInvalidEvolution}
	ErrVersionNotFound      = &Error{Kind: KindVersionNotFound}
	ErrValueCountMismatch   = &Error{Kind: KindValueCountMismatch}
	ErrVersionLimitExceeded = &Error{Kind: KindVersionLimitExceeded}
)

// newError 构造一个带操作上下文的 *Error。
func newError(kind ErrorKind, op string, version int, format string, args ...any) *Error {
	return &Error{
		Kind:    kind,
		Op:      op,
		Version: version,
		Message: fmt.Sprintf(format, args...),
	}
}

package ontology

import "fmt"

// ErrorCode 是系统可区分报告的错误类别。
// 常量声明顺序即同一次请求中多类错误条件同时具备时的
// 报告优先级：靠前者优先，只报告其中一类。
type ErrorCode int

const (
	// ErrCodeRecordBeforeFirstFact 请求的记录时刻早于该对象任何
	// 历史事实首次出现的时刻。
	ErrCodeRecordBeforeFirstFact ErrorCode = iota
	// ErrCodeSchemaInvalidated 请求展开所依赖的属性定义版本信息
	// 已被迁移操作作废。
	ErrCodeSchemaInvalidated
	// ErrCodeContradictoryRange 请求的有效时间区间或记录时间区间
	// 自相矛盾（下界大于上界）。
	ErrCodeContradictoryRange
	// ErrCodeMigrationValidation 迁移操作校验失败：既有数据不满足
	// 新定义约束。
	ErrCodeMigrationValidation
)

// Error 是系统返回的统一错误类型。
type Error struct {
	Code ErrorCode
	Msg  string
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Msg) }

func (c ErrorCode) String() string {
	switch c {
	case ErrCodeRecordBeforeFirstFact:
		return "record-before-first-fact"
	case ErrCodeSchemaInvalidated:
		return "schema-version-invalidated"
	case ErrCodeContradictoryRange:
		return "contradictory-range"
	case ErrCodeMigrationValidation:
		return "migration-validation-failed"
	}
	return "unknown"
}

func newError(code ErrorCode, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

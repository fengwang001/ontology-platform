// Package ontologyindex 实现本体平台的属性索引增量维护子系统。
//
// 子系统消费对象属性变更流，为“按属性取值定位对象”维护倒排索引；
// 支持乱序/重复事件、索引依据字段的版本迁移（原子切换、失败回滚）、
// 并发下的可串行化语义，并对每次判定保留审计记录。
package ontologyindex

import "fmt"

// ErrorCode 区分子系统对外报告的各类互不相同的错误。
//
// 当同一次判定同时满足多个错误条件时，按 ErrDeprecatedProperty >
// ErrSwitchValidation > ErrPropertyUndefined > ErrSwitchInProgress
// 的优先级只报告其中一类（见 classifyError / engine 的具体实现）。
type ErrorCode int

const (
	// ErrDeprecatedProperty：待索引的属性字段已被类型定义迁移废弃，
	// 且迁移未指定替代字段（索引依据链终止）。
	ErrDeprecatedProperty ErrorCode = iota + 1
	// ErrSwitchValidation：索引依据字段切换的新字段约束校验失败
	// （例如既有数据在新字段上不满足唯一性）。切换整体回滚。
	ErrSwitchValidation
	// ErrPropertyUndefined：变更事件在其生效时刻所适用的类型版本中
	// 引用了尚未定义的属性。
	ErrPropertyUndefined
	// ErrSwitchInProgress：查询请求发生在索引依据字段切换尚未完成的
	// 瞬间（状态机处于 switching）。
	ErrSwitchInProgress
)

// IndexError 携带错误类别与诊断信息。
type IndexError struct {
	Code    ErrorCode
	Message string
}

func (e *IndexError) Error() string { return e.Message }

func errDeprecated(format string, args ...any) error {
	return &IndexError{Code: ErrDeprecatedProperty, Message: fmt.Sprintf(format, args...)}
}

func errValidation(format string, args ...any) error {
	return &IndexError{Code: ErrSwitchValidation, Message: fmt.Sprintf(format, args...)}
}

func errUndefined(format string, args ...any) error {
	return &IndexError{Code: ErrPropertyUndefined, Message: fmt.Sprintf(format, args...)}
}

func errInProgress(format string, args ...any) error {
	return &IndexError{Code: ErrSwitchInProgress, Message: fmt.Sprintf(format, args...)}
}

// errorPriority 给出多错误并存时的报告优先级：数字越小优先级越高。
func errorPriority(c ErrorCode) int {
	switch c {
	case ErrDeprecatedProperty:
		return 0
	case ErrSwitchValidation:
		return 1
	case ErrPropertyUndefined:
		return 2
	case ErrSwitchInProgress:
		return 3
	default:
		return 4
	}
}

// highestPriorityError 在同一次判定收集到多个错误时，按规定优先级择一报告。
func highestPriorityError(errs []error) error {
	var best error
	bestRank := 1 << 30
	for _, e := range errs {
		var ie *IndexError
		if !asIndexError(e, &ie) {
			continue
		}
		if rank := errorPriority(ie.Code); rank < bestRank {
			best, bestRank = e, rank
		}
	}
	return best
}

func asIndexError(err error, target **IndexError) bool {
	ie, ok := err.(*IndexError)
	if !ok {
		return false
	}
	*target = ie
	return true
}

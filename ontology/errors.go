package ontology

import "errors"

// 错误类别（固定优先级，数值越小优先级越高）：
//  1. ErrTypeMismatch          路径声明/链接类型不匹配
//  2. ErrInstanceNotFound      起点或终点对象实例不存在
//  3. ErrCycleDetected         路径发现环且无法在声明阶段静态排除，运行时拒绝某次变更
//  4. ErrMaintenanceFailed     增量维护更新失败，整体回滚
var (
	ErrTypeMismatch      = errors.New("type mismatch in path declaration")
	ErrInstanceNotFound  = errors.New("source or target object instance not found")
	ErrCycleDetected     = errors.New("cycle detected that cannot be statically excluded")
	ErrMaintenanceFailed = errors.New("incremental maintenance failed, rolled back")
)

// ErrorKind 描述错误类别，便于测试与日志判断。
type ErrorKind int

const (
	KindTypeMismatch ErrorKind = iota + 1
	KindInstanceNotFound
	KindCycleDetected
	KindMaintenanceFailed
)

// Priority 越小优先级越高。
func (k ErrorKind) Priority() int { return int(k) }

// AggregateError 携带类别与详细信息。
type AggregateError struct {
	Kind ErrorKind
	Err  error
	Msg  string
}

func (e *AggregateError) Error() string {
	if e.Msg != "" {
		return e.Err.Error() + ": " + e.Msg
	}
	return e.Err.Error()
}

func (e *AggregateError) Unwrap() error { return e.Err }

func newError(kind ErrorKind, msg string) *AggregateError {
	var base error
	switch kind {
	case KindTypeMismatch:
		base = ErrTypeMismatch
	case KindInstanceNotFound:
		base = ErrInstanceNotFound
	case KindCycleDetected:
		base = ErrCycleDetected
	default:
		base = ErrMaintenanceFailed
	}
	return &AggregateError{Kind: kind, Err: base, Msg: msg}
}

// 便捷构造函数（各子系统统一使用，保证错误类别与 sentinel 可 errors.Is）。
func NewTypeMismatchError(msg string) *AggregateError     { return newError(KindTypeMismatch, msg) }
func NewInstanceNotFoundError(msg string) *AggregateError { return newError(KindInstanceNotFound, msg) }
func NewCycleDetectedError(msg string) *AggregateError    { return newError(KindCycleDetected, msg) }
func NewMaintenanceFailedError(msg string) *AggregateError {
	return newError(KindMaintenanceFailed, msg)
}

// HighestPriority 在多个候选错误中按固定优先级返回最高者；无候选时返回 nil。
func HighestPriority(errs ...*AggregateError) *AggregateError {
	var best *AggregateError
	for _, e := range errs {
		if e == nil {
			continue
		}
		if best == nil || e.Kind.Priority() < best.Kind.Priority() {
			best = e
		}
	}
	return best
}

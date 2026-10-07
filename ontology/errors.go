package ontology

import "errors"

// 错误类别，优先级从高到低：
// 对象实例不存在 > 属性不支持索引 > 索引维护失败 > 批量写入中其他实例导致的整体回滚。
var (
	ErrInstanceNotFound     = errors.New("instance not found")
	ErrPropertyNotIndexable = errors.New("property is not indexed")
	ErrIndexMaintenance     = errors.New("index maintenance failed")
	ErrBatchRolledBack      = errors.New("batch rolled back due to another instance")
)

// OpError 携带错误类别与上下文，便于按类别判定优先级。
type OpError struct {
	Kind       error
	InstanceID string
	PropertyID string
	IndexID    string
	Detail     string
}

func (e *OpError) Error() string {
	return e.Kind.Error() + ": instance=" + e.InstanceID +
		" property=" + e.PropertyID + " index=" + e.IndexID + " " + e.Detail
}

func (e *OpError) Unwrap() error { return e.Kind }

// errKind 返回错误的类别（用于优先级比较）。
func errKind(err error) error {
	switch {
	case errors.Is(err, ErrInstanceNotFound):
		return ErrInstanceNotFound
	case errors.Is(err, ErrPropertyNotIndexable):
		return ErrPropertyNotIndexable
	case errors.Is(err, ErrIndexMaintenance):
		return ErrIndexMaintenance
	case errors.Is(err, ErrBatchRolledBack):
		return ErrBatchRolledBack
	default:
		return nil
	}
}

// kindRank 返回错误类别优先级，数值越小优先级越高。
func kindRank(kind error) int {
	switch kind {
	case ErrInstanceNotFound:
		return 0
	case ErrPropertyNotIndexable:
		return 1
	case ErrIndexMaintenance:
		return 2
	case ErrBatchRolledBack:
		return 3
	default:
		return 4
	}
}

// HighestPriorityError 在多个错误中按固定优先级挑选应报告的错误。
func HighestPriorityError(errs []error) error {
	var best error
	for _, err := range errs {
		if err == nil {
			continue
		}
		if best == nil || kindRank(errKind(err)) < kindRank(errKind(best)) {
			best = err
		}
	}
	return best
}

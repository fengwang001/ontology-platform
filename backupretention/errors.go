package backupretention

import "errors"

// ErrorKind 对可区分的错误类别进行编号。
type ErrorKind int

const (
	// KindUnknown 未分类错误（正常流程不应产生）。
	KindUnknown ErrorKind = iota
	// KindInvalidParam 参数非法。
	KindInvalidParam
	// KindClockSkew 时钟回退：操作时刻早于上一次被接受操作的时刻。
	KindClockSkew
	// KindDuplicateID 备份标识重复。
	KindDuplicateID
	// KindParentNotFound 增量备份指定的父备份不存在。
	KindParentNotFound
	// KindTimeContradiction 时序矛盾：子备份创建时刻早于父备份。
	KindTimeContradiction
	// KindBackupNotFound 备份不存在。
	KindBackupNotFound
	// KindLayerOutOfRange 保留层数量越界（允许范围 0..1000）。
	KindLayerOutOfRange
)

// ServiceError 是本服务所有可预期错误的具体类型。
type ServiceError struct {
	Kind ErrorKind
	Op   string
	Msg  string
}

func (e *ServiceError) Error() string {
	if e == nil {
		return ""
	}
	return "backupretention: " + e.Op + ": " + e.Msg
}

func newError(kind ErrorKind, op, msg string) *ServiceError {
	return &ServiceError{Kind: kind, Op: op, Msg: msg}
}

// ErrorKindOf 返回 err 对应的错误类别；err 为 nil 时返回 KindUnknown。
func ErrorKindOf(err error) ErrorKind {
	if err == nil {
		return KindUnknown
	}
	var se *ServiceError
	if errors.As(err, &se) {
		return se.Kind
	}
	return KindUnknown
}

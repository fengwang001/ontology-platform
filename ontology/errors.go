package ontology

import "errors"

// ErrorKind 是平台对外暴露的、可程序化区分的错误类别。
type ErrorKind string

const (
	// KindObjectTypeNotFound 发起导出时对象类型不存在。
	KindObjectTypeNotFound ErrorKind = "OBJECT_TYPE_NOT_FOUND"
	// KindSubjectNotFound 发起导出时执行主体不存在。
	KindSubjectNotFound ErrorKind = "SUBJECT_NOT_FOUND"
	// KindExportNotFound 审计溯源时历史导出记录不存在。
	KindExportNotFound ErrorKind = "EXPORT_NOT_FOUND"
	// KindAttributeNotExcluded 审计溯源时该属性在该次导出中并未被排除。
	KindAttributeNotExcluded ErrorKind = "ATTRIBUTE_NOT_EXCLUDED"
)

// Error 携带稳定的错误类别与可读信息。
type Error struct {
	Kind ErrorKind
	msg  string
}

func (e *Error) Error() string { return string(e.Kind) + ": " + e.msg }

func newError(kind ErrorKind, msg string) *Error {
	return &Error{Kind: kind, msg: msg}
}

// KindOf 返回错误对应的稳定类别；非平台错误返回空串。
func KindOf(err error) ErrorKind {
	var pe *Error
	if errors.As(err, &pe) {
		return pe.Kind
	}
	return ""
}

package dlabel

// ErrorCode 是本模块所有错误的固定编号。
type ErrorCode int

const (
	CodeOK ErrorCode = iota
	// 以下三类错误具有固定且唯一的汇报优先顺序（序号即优先级，小者优先）：
	CodeMissingAttribute    // 判定规则引用了不存在的属性
	CodeCyclicRule          // 判定规则构成标签判定循环依赖
	CodeSnapshotUnavailable // 可重复读快照已不再可用

	CodeNotAdmin
	CodeUnknownObjectType
	CodeUnknownAttribute
	CodeUnknownInstance
	CodeUnknownTag
	CodeInvalidRule
	CodePermissionDenied
	CodeNotVisible
	CodeSnapshotReleased
	CodeInvalidValue
)

// Error 携带错误码与诊断信息。errors.Is 按错误码匹配哨兵错误。
type Error struct {
	Code ErrorCode
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

var (
	ErrMissingAttribute    = &Error{Code: CodeMissingAttribute, Msg: "rule references a missing attribute"}
	ErrCyclicRule          = &Error{Code: CodeCyclicRule, Msg: "cyclic tag rule dependency"}
	ErrSnapshotUnavailable = &Error{Code: CodeSnapshotUnavailable, Msg: "snapshot is no longer available"}
	ErrNotAdmin            = &Error{Code: CodeNotAdmin, Msg: "subject is not an administrator"}
	ErrUnknownObjectType   = &Error{Code: CodeUnknownObjectType, Msg: "unknown object type"}
	ErrUnknownAttribute    = &Error{Code: CodeUnknownAttribute, Msg: "unknown attribute"}
	ErrUnknownInstance     = &Error{Code: CodeUnknownInstance, Msg: "unknown instance"}
	ErrUnknownTag          = &Error{Code: CodeUnknownTag, Msg: "unknown tag"}
	ErrInvalidRule         = &Error{Code: CodeInvalidRule, Msg: "invalid rule"}
	ErrPermissionDenied    = &Error{Code: CodePermissionDenied, Msg: "permission denied"}
	ErrNotVisible          = &Error{Code: CodeNotVisible, Msg: "instance is not visible"}
	ErrSnapshotReleased    = &Error{Code: CodeSnapshotReleased, Msg: "snapshot has been released"}
	ErrInvalidValue        = &Error{Code: CodeInvalidValue, Msg: "invalid attribute value"}
)

func errorCode(err error) ErrorCode {
	if err == nil {
		return CodeOK
	}
	if e, ok := err.(*Error); ok {
		return e.Code
	}
	return -1
}

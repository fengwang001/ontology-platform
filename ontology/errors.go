package ontology

import "fmt"

// ErrorKind 是可区分的错误类别。规范要求各类错误彼此不得被误报为另一类，
// 因此所有判定错误都携带显式类别，调用方可用 errors.As 或 Kind() 判别。
type ErrorKind string

const (
	// ErrObjectNotFound 对象不存在（拒绝优先级最高）。
	ErrObjectNotFound ErrorKind = "object_not_found"
	// ErrVersionExpired 写入携带的 schema 版本不是当前最新版本。
	ErrVersionExpired ErrorKind = "version_expired"
	// ErrTypeNoPermission 类型级无读权限。
	ErrTypeNoPermission ErrorKind = "type_no_permission"
	// ErrAttrNotFound 属性标识符不存在（因废弃、弹性重命名后引用旧名字，或从未存在）。
	ErrAttrNotFound ErrorKind = "attr_not_found"
	// ErrRevoked 显式吊销记录覆盖该版本区间，吊销优先于授权并集。
	ErrRevoked ErrorKind = "revoked"
	// ErrWritePolicyConflict 同一类型试图混用“整条拒绝”与“整体忽略”两种写入策略。
	ErrWritePolicyConflict ErrorKind = "write_policy_conflict"
	// ErrPermissionDenied 属性级无权限（非吊销原因）。
	ErrPermissionDenied ErrorKind = "permission_denied"
	// ErrInvalidArgument 输入不合法（未知类型、策略为空、区间非法等）。
	ErrInvalidArgument ErrorKind = "invalid_argument"
	// ErrTypeNotFound 对象类型不存在。
	ErrTypeNotFound ErrorKind = "type_not_found"
)

// DecisionError 携带类别的判定错误，保证错误可区分。
type DecisionError struct {
	Kind ErrorKind
	Msg  string
}

func (e *DecisionError) Error() string {
	return fmt.Sprintf("%s: %s", e.Kind, e.Msg)
}

func newError(kind ErrorKind, format string, args ...any) *DecisionError {
	return &DecisionError{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}

// ErrorKindOf 提取错误类别；非本包错误返回空串。
func ErrorKindOf(err error) ErrorKind {
	if err == nil {
		return ""
	}
	if de, ok := err.(*DecisionError); ok {
		return de.Kind
	}
	return ""
}

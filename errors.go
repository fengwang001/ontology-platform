package ontology

import "fmt"

// ErrorKind 是本子系统对外暴露的、可相互区分的错误类别。
type ErrorKind string

const (
	// ErrInvalidArgument: 参数非法（主键/分组键为空、属性值类型不符）。
	ErrInvalidArgument ErrorKind = "invalid_argument"
	// ErrVersionConflict: 乐观并发凭证（前序版本号）不匹配。
	ErrVersionConflict ErrorKind = "version_conflict"
	// ErrNotFound: 主键从未成功提交过任何版本，删除被拒绝。
	ErrNotFound ErrorKind = "not_found"
	// ErrAlreadyDeleted: 主键存在但已被删除，对其再次删除被拒绝。
	ErrAlreadyDeleted ErrorKind = "already_deleted"
)

// Error 是经错误归一化模块统一后的规范错误。
type Error struct {
	Kind    ErrorKind
	Message string
}

func (e *Error) Error() string {
	return string(e.Kind) + ": " + e.Message
}

// 错误归一化模块：所有内部原因在此处收敛为规范错误，保证三类（实为四类）
// 拒绝原因可相互区分，且拒绝次序由内核负责：
// 参数非法 -> 乐观凭证不匹配 -> 不存在/已删除。
func invalidArgumentf(format string, args ...any) *Error {
	return &Error{Kind: ErrInvalidArgument, Message: fmt.Sprintf(format, args...)}
}

func versionConflictf(format string, args ...any) *Error {
	return &Error{Kind: ErrVersionConflict, Message: fmt.Sprintf(format, args...)}
}

func notFoundf(format string, args ...any) *Error {
	return &Error{Kind: ErrNotFound, Message: fmt.Sprintf(format, args...)}
}

func alreadyDeletedf(format string, args ...any) *Error {
	return &Error{Kind: ErrAlreadyDeleted, Message: fmt.Sprintf(format, args...)}
}

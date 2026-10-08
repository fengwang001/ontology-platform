package ontology

import "fmt"

// ErrorKind 区分写入/变更失败的类别，调用方可据此做不同处理。
type ErrorKind int

const (
	ErrObjectNotFound ErrorKind = iota
	ErrVersionConflict
	ErrPermissionDenied
	ErrMissingRequired
	ErrTypeNotFound
	ErrCycle
)

func (k ErrorKind) String() string {
	switch k {
	case ErrObjectNotFound:
		return "object_not_found"
	case ErrVersionConflict:
		return "version_conflict"
	case ErrPermissionDenied:
		return "permission_denied"
	case ErrMissingRequired:
		return "missing_required_property"
	case ErrTypeNotFound:
		return "type_not_found"
	case ErrCycle:
		return "inheritance_cycle"
	}
	return "unknown"
}

// Error 是协调器返回的统一错误，Kind 可区分，Detail 给人看。
type Error struct {
	Kind   ErrorKind
	Detail string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Kind, e.Detail)
}

func newError(kind ErrorKind, format string, args ...any) *Error {
	return &Error{Kind: kind, Detail: fmt.Sprintf(format, args...)}
}

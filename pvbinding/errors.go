package pvbinding

import "fmt"

// ErrorKind 错误类别，优先级从高到低：
// ErrInvalidArgument > ErrNotFound > ErrConflict > ErrNoMatch > ErrCapacityExceeded。
type ErrorKind string

const (
	// ErrInvalidArgument 参数非法（最高优先级）。
	ErrInvalidArgument ErrorKind = "InvalidArgument"
	// ErrNotFound 对象不存在。
	ErrNotFound ErrorKind = "NotFound"
	// ErrConflict 状态冲突。
	ErrConflict ErrorKind = "Conflict"
	// ErrNoMatch 无满足的卷。
	ErrNoMatch ErrorKind = "NoMatchingVolume"
	// ErrCapacityExceeded 扩容容量超过所绑卷容量。
	ErrCapacityExceeded ErrorKind = "CapacityExceeded"
)

// ControllerError 携带可区分错误类别的错误。
type ControllerError struct {
	Kind   ErrorKind
	Op     string
	Detail string
}

func (e *ControllerError) Error() string {
	return fmt.Sprintf("%s: %s: %s", e.Op, e.Kind, e.Detail)
}

// KindOf 返回错误对应的 ErrorKind；非控制器错误返回空串。
func KindOf(err error) ErrorKind {
	if e, ok := err.(*ControllerError); ok {
		return e.Kind
	}
	return ""
}

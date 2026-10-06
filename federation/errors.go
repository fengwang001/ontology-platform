package federation

import "fmt"

// ErrorCode 表示可区分的错误类别。数值越小优先级越高：
// 参数非法 > 配置冲突 > 最小副本之和超出总数 > 容量不足。
type ErrorCode int

const (
	// ErrInvalidParam 参数非法（最高优先级）。
	ErrInvalidParam ErrorCode = iota + 1
	// ErrConfigConflict 配置冲突：某参与分配的集群最小副本数大于其有效上限。
	ErrConfigConflict
	// ErrMinExceedsTotal 所有参与分配集群的最小副本数之和大于总副本数。
	ErrMinExceedsTotal
	// ErrInsufficientCapacity 所有参与集群饱和后仍有剩余副本。
	ErrInsufficientCapacity
)

func (c ErrorCode) String() string {
	switch c {
	case ErrInvalidParam:
		return "invalid parameter"
	case ErrConfigConflict:
		return "configuration conflict"
	case ErrMinExceedsTotal:
		return "sum of minimum replicas exceeds total"
	case ErrInsufficientCapacity:
		return "insufficient capacity"
	default:
		return "unknown error"
	}
}

// AllocError 携带错误类别与可读详情。一次请求只返回一个 AllocError，
// 当多个问题同时存在时，固定选择优先级最高的类别。
type AllocError struct {
	Code ErrorCode
	Msg  string
}

func (e *AllocError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Msg)
}

func invalidParam(format string, args ...any) error {
	return &AllocError{Code: ErrInvalidParam, Msg: fmt.Sprintf(format, args...)}
}

func configConflict(format string, args ...any) error {
	return &AllocError{Code: ErrConfigConflict, Msg: fmt.Sprintf(format, args...)}
}

func minExceedsTotal(format string, args ...any) error {
	return &AllocError{Code: ErrMinExceedsTotal, Msg: fmt.Sprintf(format, args...)}
}

func insufficientCapacity(format string, args ...any) error {
	return &AllocError{Code: ErrInsufficientCapacity, Msg: fmt.Sprintf(format, args...)}
}

package keygroup

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidGroups 表示构造时键组数量非法（必须为正整数）。
	ErrInvalidGroups = errors.New("keygroup: invalid key-group count: must be positive")
	// ErrInvalidParallelism 表示构造时初始并行度非法（必须为正整数）。
	ErrInvalidParallelism = errors.New("keygroup: invalid parallelism: must be positive")
	// ErrParallelismTooLarge 表示调整后的并行度超过键组数量。
	ErrParallelismTooLarge = errors.New("keygroup: parallelism exceeds key-group count")
	// ErrParallelismUnchanged 表示调整目标与当前并行度相同，无需迁移。
	ErrParallelismUnchanged = errors.New("keygroup: parallelism unchanged")
	// ErrEmptyKey 表示读写的键为空串。
	ErrEmptyKey = errors.New("keygroup: empty key is not allowed")
	// ErrInvalidLogger 表示构造时传入了空日志器。
	ErrInvalidLogger = errors.New("keygroup: nil logger")
)

// InvalidGroupsError 携带被拒绝的键组数量。
type InvalidGroupsError struct {
	Groups int
}

func (e *InvalidGroupsError) Error() string {
	return fmt.Sprintf("%v: got %d", ErrInvalidGroups, e.Groups)
}
func (e *InvalidGroupsError) Unwrap() error { return ErrInvalidGroups }

// InvalidParallelismError 携带被拒绝的并行度及其来源阶段。
type InvalidParallelismError struct {
	Parallelism int
	Reason      error
}

func (e *InvalidParallelismError) Error() string {
	return fmt.Sprintf("%v: got %d", e.Reason, e.Parallelism)
}
func (e *InvalidParallelismError) Unwrap() error { return e.Reason }

// GroupOutOfRangeError 表示键组编号越界。
type GroupOutOfRangeError struct {
	Group     int
	NumGroups int
}

func (e *GroupOutOfRangeError) Error() string {
	return fmt.Sprintf("keygroup: key-group %d out of range [0, %d)", e.Group, e.NumGroups)
}

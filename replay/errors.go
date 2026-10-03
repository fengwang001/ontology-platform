// Package replay 在 history 日志与 code 代码之间做确定性重放：
// 取一次历史快照后逐条比对，历史耗尽处续跑生成新事件，并依据
// Branch(pid, N, O) 的补丁标记决定走新分支还是旧分支。
package replay

import (
	"errors"
	"fmt"
)

// 三类非确定性错误的哨兵。errors.Is(err, ErrMismatch) 等可区分类别，
// IndexFrom(err) 可取出出错的历史下标。
var (
	// ErrMismatch：下标 i 处历史为 S(x) 而代码要执行 S(y)，x≠y。
	ErrMismatch = errors.New("replay: step mismatch")

	// ErrUnexpectedMarker：下标 i 处历史为 M(q)，而代码要执行 Step，
	// 或要执行 Branch(pid) 且 q≠pid。
	ErrUnexpectedMarker = errors.New("replay: unexpected patch marker")

	// ErrHistoryExtra：代码已跑完但历史未耗尽，i 为首个未消费事件的下标。
	ErrHistoryExtra = errors.New("replay: unconsumed history events")
)

// ErrArgument 表示 Run 的参数非法（wf/name/pid 为空、代码项数越界等），
// 与 code.ErrArgument、history.ErrArgument 语义一致，errors.Is 均能命中。
var ErrArgument = errors.New("replay: invalid argument")

// IndexError 携带出错下标的非确定性错误。不要直接构造；
// 通过 errors.Is 判断类别、IndexFrom 取下标。
type IndexError struct {
	kind  error
	index int
}

func (e *IndexError) Error() string {
	return fmt.Sprintf("%s at index %d", e.kind.Error(), e.index)
}

// Is 使 errors.Is 能穿透到哨兵错误（ErrMismatch / ErrUnexpectedMarker /
// ErrHistoryExtra）。
func (e *IndexError) Is(target error) bool { return target == e.kind }

// Unwrap 返回哨兵错误，便于 errors.Is/errors.As 的常规链路。
func (e *IndexError) Unwrap() error { return e.kind }

// Index 返回出错的历史下标 c。
func (e *IndexError) Index() int { return e.index }

// IndexFrom 从错误中取出非确定性错误的下标；
// 若 err 不是三类非确定性错误之一，ok 为 false。
func IndexFrom(err error) (i int, ok bool) {
	var idx *IndexError
	if errors.As(err, &idx) {
		return idx.index, true
	}
	return 0, false
}

// Package replay 把代码产出的命令序列与持久化历史逐条比对，
// 支持补丁分支，并在不一致时给出可区分的非确定性错误。
package replay

import (
	"errors"
	"fmt"
)

// 三类非确定性错误的类别哨兵，可用 errors.Is 区分；
// 具体错误值携带下标，可用 errors.As 取出。
var (
	// ErrMismatch 表示 c=i 处历史为 S(x) 而代码要 S(y)，x!=y。
	ErrMismatch = errors.New("replay: step mismatch")
	// ErrUnexpectedMarker 表示 c=i 处历史为 M(q)，而代码要执行
	// Step，或要执行 Branch(pid) 且 q!=pid。
	ErrUnexpectedMarker = errors.New("replay: unexpected patch marker")
	// ErrHistoryExtra 表示代码跑完而 c=i<L，历史有多余事件。
	ErrHistoryExtra = errors.New("replay: history has extra events")
)

// MismatchError 是 ErrMismatch 的具体形式。
type MismatchError struct {
	Index int    // 发生冲突的历史下标 i
	Have  []byte // 历史中的步骤名 x
	Want  []byte // 代码要求的步骤名 y
}

func (e *MismatchError) Error() string {
	return fmt.Sprintf("%v at %d: history has S(%s), code wants S(%s)",
		ErrMismatch, e.Index, e.Have, e.Want)
}

// Is 使 errors.Is(err, ErrMismatch) 成立。
func (e *MismatchError) Is(target error) bool { return target == ErrMismatch }

// UnexpectedMarkerError 是 ErrUnexpectedMarker 的具体形式。
type UnexpectedMarkerError struct {
	Index int    // 遇到标记的历史下标 i
	Pid   []byte // 历史中的标记 pid（q）
}

func (e *UnexpectedMarkerError) Error() string {
	return fmt.Sprintf("%v at %d: history has M(%s)", ErrUnexpectedMarker, e.Index, e.Pid)
}

// Is 使 errors.Is(err, ErrUnexpectedMarker) 成立。
func (e *UnexpectedMarkerError) Is(target error) bool {
	return target == ErrUnexpectedMarker
}

// HistoryExtraError 是 ErrHistoryExtra 的具体形式。
type HistoryExtraError struct {
	Index int // 首个未消费事件的下标 i
}

func (e *HistoryExtraError) Error() string {
	return fmt.Sprintf("%v at %d: first unconsumed event", ErrHistoryExtra, e.Index)
}

// Is 使 errors.Is(err, ErrHistoryExtra) 成立。
func (e *HistoryExtraError) Is(target error) bool { return target == ErrHistoryExtra }

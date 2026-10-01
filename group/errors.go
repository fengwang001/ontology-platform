package group

import "errors"

// 可被 errors.Is 区分的拒绝原因。
var (
	// ErrClockBackwards 表示调用方传入的 now 小于此前任一次调用传入的 now。
	ErrClockBackwards = errors.New("group: clock moved backwards")
	// ErrUnknownMember 表示成员不在当前组的已知成员集合中。
	ErrUnknownMember = errors.New("group: unknown member")
	// ErrStaleGeneration 表示请求携带的代数与当前代数不等。
	ErrStaleGeneration = errors.New("group: stale generation")
	// ErrRejoinNeeded 表示组处于准备中，成员需要重新加入。
	ErrRejoinNeeded = errors.New("group: rebalance in progress, rejoin needed")
	// ErrNotReady 表示分配尚未就绪（等待分配状态下的查询）。
	ErrNotReady = errors.New("group: assignment not ready")
	// ErrNotLeader 表示非领导者在等待分配状态下提交了非空分配表。
	ErrNotLeader = errors.New("group: non-leader submitted assignment")
	// ErrAssignmentMismatch 表示分配表的成员集合与本代成员集合不一致。
	ErrAssignmentMismatch = errors.New("group: assignment member set mismatch")
)

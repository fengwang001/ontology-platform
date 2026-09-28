package lwwset

import "errors"

// 所有错误均为哨兵错误，可用 errors.Is 精确区分拒绝原因。
var (
	// ErrNilReplica 表示传入的副本指针为 nil。
	ErrNilReplica = errors.New("lwwset: replica must not be nil")
	// ErrInvalidReplicaID 表示副本编号不是正整数。
	ErrInvalidReplicaID = errors.New("lwwset: replica id must be positive")
	// ErrEmptyElement 表示元素为空字符串。
	ErrEmptyElement = errors.New("lwwset: element must not be empty string")
	// ErrNonPositiveTimestamp 表示时间戳不是正整数（<=0）。
	ErrNonPositiveTimestamp = errors.New("lwwset: timestamp must be positive")
	// ErrInvalidLimit 表示记录元素数上限不是正整数。
	ErrInvalidLimit = errors.New("lwwset: element record limit must be positive")
	// ErrLimitExceeded 表示一次操作或合并会使不同元素的记录条数超过上限。
	ErrLimitExceeded = errors.New("lwwset: distinct element record count would exceed limit")
	// ErrSelfMerge 表示把一个副本与自身合并。
	ErrSelfMerge = errors.New("lwwset: cannot merge a replica into itself")
	// ErrInvalidSince 表示增量起点序号为负数。
	ErrInvalidSince = errors.New("lwwset: since sequence number must not be negative")
	// ErrSequenceGap 表示增量流的序号与目标已合并位置不连续。
	ErrSequenceGap = errors.New("lwwset: change stream sequence is not contiguous with merge position")
	// ErrMissingTimestamp 表示增量流中的变更缺少添加或删除标记。
	ErrMissingTimestamp = errors.New("lwwset: change entry must carry an add or remove timestamp")
)

package lwwset

import "errors"

var (
	// ErrNilReplica 表示合并的源副本为 nil。
	ErrNilReplica = errors.New("lwwset: 源副本为 nil")
	// ErrSelfMerge 表示副本与自身合并。
	ErrSelfMerge = errors.New("lwwset: 副本不能与自身合并")
	// ErrInvalidReplicaID 表示副本编号为空。
	ErrInvalidReplicaID = errors.New("lwwset: 副本编号为空")
	// ErrEmptyElement 表示元素为空串。
	ErrEmptyElement = errors.New("lwwset: 元素为空串")
	// ErrNonPositiveTimestamp 表示时间戳非正。
	ErrNonPositiveTimestamp = errors.New("lwwset: 时间戳必须为正整数")
	// ErrTooManyElements 表示记录元素数超过上限。
	ErrTooManyElements = errors.New("lwwset: 记录元素数超过上限")
	// ErrInvalidLimit 表示构造时元素上限非正。
	ErrInvalidLimit = errors.New("lwwset: 元素上限必须为正整数")
)

package snapshot

import "errors"

// 互不相同、可区分的输入拒绝原因。
var (
	// ErrInvalidArgument 参数本身非法（nil、空文件名、负数时间语义、
	// retainCount 为负、added/removed 内部重复或两者交集非空等）。
	ErrInvalidArgument = errors.New("snapshot: invalid argument")

	// ErrTimeNotIncreasing 提交时间未严格晚于链上最新快照时间。
	ErrTimeNotIncreasing = errors.New("snapshot: commit time must be strictly increasing")

	// ErrFileNameReused 新增文件名在表生命周期内曾经使用过（永不复用）。
	ErrFileNameReused = errors.New("snapshot: added file name reused")

	// ErrFileNotInCurrent 被移除的文件名不在当前快照文件集内。
	ErrFileNotInCurrent = errors.New("snapshot: removed file not present in current snapshot")

	// ErrSnapshotNotFound 查询的快照不存在（已过期或从未创建）。
	ErrSnapshotNotFound = errors.New("snapshot: snapshot not found")
)

// Package snapshot 实现表快照过期与可删除数据文件判定。
package snapshot

import "errors"

// 错误类别（可用 errors.Is 区分，互不相同）：
var (
	// ErrInvalidArgument 非法参数：文件名为空、新增/移除列表内部重复、
	// 新增与移除存在交集、保留数量为负等。
	ErrInvalidArgument = errors.New("snapshot: invalid argument")
	// ErrTimestampNotIncreasing 提交时间未严格递增。
	ErrTimestampNotIncreasing = errors.New("snapshot: commit timestamp not strictly increasing")
	// ErrFileNameConflict 新增文件名已被历史使用过（文件名永不复用）。
	ErrFileNameConflict = errors.New("snapshot: file name already used")
	// ErrRemoveNotInSnapshot 移除项不在当前快照文件集内。
	ErrRemoveNotInSnapshot = errors.New("snapshot: removed file not in current snapshot")
)

package shallow

import "errors"

// CommitID 是提交的唯一标识。
type CommitID string

// BlobID 是内容对象的唯一标识。
type BlobID string

// Blob 是内容对象：只有标识与大小（字节）。
type Blob struct {
	ID   BlobID
	Size int64
}

// Commit 是远端完整提交图中的一个提交。
type Commit struct {
	ID        CommitID
	Parents   []CommitID
	CreatedAt int64
	Blobs     []BlobID
}

// Snapshot 是载入本地仓库时给定的初始本地状态。
type Snapshot struct {
	Commits  []Commit
	Blobs    []Blob
	Refs     map[string]CommitID
	Boundary []CommitID
}

// GCResult 是一次回收删除的对象统计。
type GCResult struct {
	Objects int
	Bytes   int64
}

// 错误约定：调用方可用 errors.Is 区分；错误优先级见 DESIGN.md。
var (
	// ErrInvalidArg 参数非法（深度非正、引用名为空、时刻为负）。
	ErrInvalidArg = errors.New("shallow: invalid argument")
	// ErrRefNotFound 引用不存在。
	ErrRefNotFound = errors.New("shallow: ref not found")
	// ErrRemoteMissing 远端不存在该提交（与拉取失败可区分）。
	ErrRemoteMissing = errors.New("shallow: remote object does not exist")
	// ErrRemoteFetch 远端不可用或拉取中途失败。

	ErrRemoteFetch = errors.New("shallow: remote fetch failed")
	// ErrIllegalState 本地状态非法（边界外提交缺父等）。
	ErrIllegalState = errors.New("shallow: illegal local state")
)

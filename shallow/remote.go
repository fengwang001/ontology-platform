package shallow

import "context"

// Remote 抽象远端完整提交图。
// FetchCommit 以提交为单位原子返回该提交与其全部直接引用内容对象。
// 返回 ErrRemoteMissing 表示远端确定不存在该提交；
// 返回 ErrRemoteFetch 表示远端不可用或传输中途失败。
type Remote interface {
	FetchCommit(ctx context.Context, id CommitID) (Commit, []Blob, error)
}

package blockstore

import "errors"

// BlockState 是块在库中的生命周期状态。
type BlockState int

const (
	// StateNormal 正常块：至少被一份已提交清单引用，或在首次上传后处于候选状态。
	StateNormal BlockState = iota
	// StatePending 待删块：第一轮回收标记，仍可被读取和复用。
	StatePending
	// StateDeleted 已删块：物理存储中不再存在。
	StateDeleted
)

func (s BlockState) String() string {
	switch s {
	case StateNormal:
		return "normal"
	case StatePending:
		return "pending"
	case StateDeleted:
		return "deleted"
	default:
		return "unknown"
	}
}

// 可区分的提交 / 操作失败原因。
var (
	// ErrBlockMissing 清单引用了既未上传、库中也不存在（或已删除）的块。
	ErrBlockMissing = errors.New("blockstore: referenced block is missing")
	// ErrSessionNotFound 会话不存在。
	ErrSessionNotFound = errors.New("blockstore: session not found")
	// ErrSessionClosed 会话已经结束，不能再提交。
	ErrSessionClosed = errors.New("blockstore: session already closed")
	// ErrAlreadyCommitted 同一会话重复提交。
	ErrAlreadyCommitted = errors.New("blockstore: session already committed")
	// ErrCapacityFull 存储容量已满，无法接受新块。
	ErrCapacityFull = errors.New("blockstore: storage capacity full")
	// ErrGCInProgress 已有一轮两轮回收尚未完成。
	ErrGCInProgress = errors.New("blockstore: garbage collection cycle already in progress")
	// ErrManifestNotFound 清单不存在。
	ErrManifestNotFound = errors.New("blockstore: manifest not found")
)

// CommitError 让调用方可以程序化区分提交被拒绝的具体原因。
type CommitError struct {
	Reason error
	Detail string
}

func (e *CommitError) Error() string {
	if e.Detail == "" {
		return e.Reason.Error()
	}
	return e.Reason.Error() + ": " + e.Detail
}

func (e *CommitError) Unwrap() error { return e.Reason }

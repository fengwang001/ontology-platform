package lineage

import (
	"errors"
	"fmt"
)

// 按拒绝次序排列的哨兵错误。
var (
	ErrKeyOutOfRange        = errors.New("lineage: key out of range")
	ErrClockBackward        = errors.New("lineage: clock cannot move backward")
	ErrShardNotFound        = errors.New("lineage: shard not found")
	ErrShardClosed          = errors.New("lineage: shard already closed")
	ErrSplitPointOutOfRange = errors.New("lineage: split point out of range")
	ErrShardsNotAdjacent    = errors.New("lineage: shards are not adjacent")
	ErrShardDrained         = errors.New("lineage: shard already drained")
	ErrParentsUndrained     = errors.New("lineage: shard has undrained parents")
	ErrAlreadyHeld          = errors.New("lineage: shard already has a valid holder")
	ErrWorkerLeaseLimit     = errors.New("lineage: worker holds too many valid leases")
	ErrNotValidHolder       = errors.New("lineage: caller is not the valid lease holder")
	ErrProgressBackward     = errors.New("lineage: committed progress must not move backward")
	ErrProgressBeyondAppend = errors.New("lineage: committed progress must not exceed appended count")
)

// GrantError 表示领取租约失败。当原因是存在未排空父分片时，
// UndrainedParents 列出全部未排空父分片 ID（按升序）。
type GrantError struct {
	Err              error
	UndrainedParents []ShardID
}

func (e *GrantError) Error() string {
	if len(e.UndrainedParents) > 0 {
		return fmt.Sprintf("%v (undrained parents: %v)", e.Err, e.UndrainedParents)
	}
	return e.Err.Error()
}

func (e *GrantError) Unwrap() error { return e.Err }

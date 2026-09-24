// Package visible 判定一个版本对读快照是否可见。
package visible

import (
	"errors"

	"ontology/snapshot"
	"ontology/txn"
)

// ErrCorruptChain 表示版本链上相邻版本的提交号不是严格递增的。
var ErrCorruptChain = errors.New("visible: version chain commit numbers must strictly increase")

// Version 是版本链上的一个版本，由某事务写入并携带其提交号。
type Version struct {
	Txn      txn.ID
	CommitNo uint64
}

// Judge 对单个快照执行可见性判定。一次判定持有独立计数器，
// 因此多个 goroutine 各自使用自己的 Judge 时计数器互不串台。
type Judge struct {
	snap *snapshot.Snapshot
	reg  *txn.Registry

	// lookups 记录最近一次判定执行的常数次查找次数。
	lookups int
}

// NewJudge 为给定快照与事务表创建判定器。
func NewJudge(snap *snapshot.Snapshot, reg *txn.Registry) *Judge {
	return &Judge{snap: snap, reg: reg}
}

// Lookups 返回最近一次 Visible 调用执行的查找次数。
func (j *Judge) Lookups() int { return j.lookups }

// Visible 判定版本 v 对快照是否可见。单次判定至多做 2 次查找：
// 一次事务表查找、一次活跃集查找，不遍历事务表。
func (j *Judge) Visible(v Version) (bool, error) {
	j.lookups = 0
	if j.snap.Released() {
		return false, snapshot.ErrSnapshotReleased
	}
	if v.Txn == j.snap.Self() {
		return true, nil // 自己写的自己可见
	}
	state, commitNo, err := j.reg.Info(v.Txn)
	j.lookups++
	if err != nil {
		return false, err // 含 txn.ErrUnknownTxn
	}
	if commitNo != v.CommitNo || state != txn.Committed {
		return false, nil // 未提交或已回滚，或提交号不自洽
	}
	if commitNo >= j.snap.Watermark() {
		return false, nil // 左闭右开：等于水位也不可见
	}
	inActive := j.snap.Contains(v.Txn)
	j.lookups++
	return !inActive, nil
}

// ValidateChain 校验版本链上各版本提交号严格递增。
func ValidateChain(chain []Version) error {
	for i := 1; i < len(chain); i++ {
		if chain[i].CommitNo <= chain[i-1].CommitNo {
			return ErrCorruptChain
		}
	}
	return nil
}

// Package visible 判定一个版本对给定读快照是否可见。
package visible

import (
	"errors"

	"ontology/snapshot"
	"ontology/txn"
)

// 三类可判定错误（可 errors.Is），外加资源上限错误一并暴露。
var (
	ErrUnknown     = txn.ErrUnknown
	ErrReleased    = snapshot.ErrReleased
	ErrCorrupt     = errors.New("visible: version chain commit numbers not decreasing")
	ErrActiveLimit = snapshot.ErrActiveSetLimit
)

// Store 是判定所需的事务表能力（*txn.Registry 天然满足）。
type Store interface {
	Lookup(txn.ID) (txn.Status, uint64, error)
}

// Version 是版本链上的一个版本，由 Txn 写入。
type Version struct {
	Txn txn.ID
}

// Decision 携带一次判定的结果与查找计数。
type Decision struct {
	Visible bool
	lookups int
}

// Lookups 返回本次判定发生的表/集合查找次数。
func (d Decision) Lookups() int { return d.lookups }

// probe 是单次判定的非导出查找计数器。
type probe struct{ n int }

func (p *probe) lookup() { p.n++ }

// Decide 按「提交号 < 水位 且 写入事务不在活跃集中（owner 豁免）」判定。
func Decide(store Store, snap *snapshot.Snapshot, v Version) (Decision, error) {
	var p probe
	if err := snap.Use(); err != nil { // 仅状态检查，不计入数据查找
		return Decision{}, err
	}
	st, commit, err := store.Lookup(v.Txn) // 查找 1：事务表
	p.lookup()
	if err != nil {
		return Decision{}, err
	}
	// 自己写的自己可见，无需再查活跃集。
	if v.Txn == snap.Owner() {
		return Decision{Visible: st == txn.Committed, lookups: p.n}, nil
	}
	switch {
	case st == txn.Active:
		return Decision{Visible: false, lookups: p.n}, nil // 进行中必不可见
	case st == txn.Aborted:
		return Decision{Visible: false, lookups: p.n}, nil // 回滚永不可见
	case commit >= snap.Watermark():
		return Decision{Visible: false, lookups: p.n}, nil // 等于水位也不可见（左闭右开）
	}
	active, err := snap.Active(v.Txn) // 查找 2：活跃集
	p.lookup()
	if err != nil {
		return Decision{}, err
	}
	return Decision{Visible: !active, lookups: p.n}, nil
}

// CheckChain 校验版本链提交号严格递减（链头最新），非递增即损坏。
func CheckChain(store Store, chain []Version) error {
	var prev uint64
	first := true
	for _, v := range chain {
		st, commit, err := store.Lookup(v.Txn)
		if err != nil {
			return err
		}
		if st != txn.Committed {
			continue
		}
		if !first && commit >= prev {
			return ErrCorrupt
		}
		prev, first = commit, false
	}
	return nil
}

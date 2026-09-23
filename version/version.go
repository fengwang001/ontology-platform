// Package version 维护单个键的版本链：追加、按快照选可见版本、删除标记与回收。
package version

import "ontology/txid"

// Version 是版本链上的一个不可变版本。
// CT 为 0 表示尚未提交（仅其属主事务可见）。
type Version struct {
	CT      txid.TXID
	Owner   txid.TXID
	Value   []byte
	Deleted bool
}

// Predicate 判断一个已提交版本对某读视图是否可见。
type Predicate func(ct txid.TXID) bool

// Result 是按快照查询一个键的三态结果。
type Result struct {
	Kind  ResultKind
	Value []byte
}

// ResultKind 区分：命中值、命中删除标记、键从未存在。
type ResultKind uint8

const (
	NotFound ResultKind = iota
	Deleted
	Value
)

// Chain 是一个键的版本链（旧→新）。gate 之前的版本已被回收。
type Chain struct {
	versions []Version
	gate     int
}

// Len 返回链上尚未回收的版本数。
func (c *Chain) Len() int { return len(c.versions) - c.gate }

// Append 在链尾追加一个属主为 owner 的未提交版本，返回其下标。
func (c *Chain) Append(owner txid.TXID, value []byte, deleted bool) int {
	c.versions = append(c.versions, Version{Owner: owner, Value: value, Deleted: deleted})
	return len(c.versions) - 1
}

// Commit 给下标 idx 的版本盖上提交事务号（提交临界区内调用）。
func (c *Chain) Commit(idx int, ct txid.TXID) { c.versions[idx].CT = ct }

// Drop 摘除链尾未提交版本（回滚时调用，仅允许摘除尾部）。
func (c *Chain) Drop(idx int) { c.versions = c.versions[:idx] }

// Lookup 自新向旧选择可见版本；self 非零时额外放行该属主的未提交版本（读己之写）。
func (c *Chain) Lookup(visible Predicate, self txid.TXID) Result {
	for i := len(c.versions) - 1; i >= c.gate; i-- {
		v := &c.versions[i]
		if v.CT.Valid() {
			if !visible(v.CT) {
				continue
			}
		} else if v.Owner != self {
			continue
		}
		if v.Deleted {
			return Result{Kind: Deleted}
		}
		return Result{Kind: Value, Value: v.Value}
	}
	return Result{Kind: NotFound}
}

// LiveCount 返回链上尚未回收版本数（含删除标记），供只读统计使用。
func (c *Chain) LiveCount() int { return len(c.versions) - c.gate }

// BlockCT 返回"遮蔽版本"（最旧存活版本的后继）的提交号。
// 该号严格小于回收 horizon 时，最旧存活版本才可被回收；链长 <2 时无候选。
func (c *Chain) BlockCT() (txid.TXID, bool) {
	if c.Len() < 2 {
		return 0, false
	}
	return c.versions[c.gate+1].CT, true
}

// At 返回下标 i 处版本（回收器遍历用）。
func (c *Chain) At(i int) Version { return c.versions[i] }

// Base 返回回收候选的起始下标。
func (c *Chain) Base() int { return c.gate }

// Advance 在 horizon 下回收被遮蔽的旧版本：
// 只要候选版本 i 的后继 i+1 提交号也严格小于 horizon，候选即被完全遮蔽，推进 gate。
// 返回实际考察的版本数（每次触碰一个候选计 1）与回收数。
func (c *Chain) Advance(horizon txid.TXID) (inspected, reclaimed int) {
	for c.Len() >= 2 {
		next := c.versions[c.gate+1]
		if !next.CT.Valid() || !next.CT.Before(horizon) {
			break
		}
		inspected++
		c.gate++
		reclaimed++
	}
	return inspected, reclaimed
}

// Reset 清空链（仅供 WAL 重放前重建）。
func (c *Chain) Reset() { c.versions = nil; c.gate = 0 }

// All 返回存活版本切片的只读访问（重放/校验用）。
func (c *Chain) All() []Version { return c.versions[c.gate:] }

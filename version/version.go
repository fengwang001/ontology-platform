// Package version 实现单个键的版本链：追加、按快照选可见版本、删除标记。
// 依赖 txid；可见性判定所需的快照信息以参数注入，不反向依赖 snapshot。
package version

import "ontology/txid"

// Version 是键的一次写入。Del 为真表示删除标记版本。
// Live 为真表示其写事务已提交（可见性的唯一判定点）。
type Version struct {
	Value []byte
	Del   bool
	Tx    txid.T
	Live  bool
}

// Chain 是单键版本链，按提交顺序追加，新的在末尾。
type Chain struct {
	vs []Version
}

// Len 返回链中版本总数。
func (c *Chain) Len() int {
	return len(c.vs)
}

// Append 追加一个版本（通常 Live=false，提交时再置位）。
func (c *Chain) Append(v Version) {
	c.vs = append(c.vs, v)
}

// Top 返回最新已提交版本的事务号；无已提交版本时 ok=false。
func (c *Chain) Top() (top txid.T, ok bool) {
	for i := len(c.vs) - 1; i >= 0; i-- {
		if c.vs[i].Live {
			return c.vs[i].Tx, true
		}
	}
	return txid.Invalid, false
}

// Visible 按快照选可见版本：从新到旧，第一个已提交、提交号 < point
// 且不在活跃集合中的版本。active 为 nil 视为无活跃事务。
func (c *Chain) Visible(point txid.T, active func(txid.T) bool) (Version, bool) {
	for i := len(c.vs) - 1; i >= 0; i-- {
		v := c.vs[i]
		if !v.Live || v.Tx >= point {
			continue
		}
		if active != nil && active(v.Tx) {
			continue
		}
		return v, true
	}
	return Version{}, false
}

// Commit 把事务 tx 追加的所有版本标记为已提交（提交记录）。
func (c *Chain) Commit(tx txid.T) {
	for i := range c.vs {
		if c.vs[i].Tx == tx {
			c.vs[i].Live = true
		}
	}
}

// Remove 摘除提交号为 commit 的已提交版本（回收用）。返回是否摘除。
// 调用方必须保证该版本已被更新版本完全遮蔽且水位允许。
func (c *Chain) Remove(commit txid.T) bool {
	for i := range c.vs {
		if c.vs[i].Live && c.vs[i].Tx == commit {
			c.vs = append(c.vs[:i], c.vs[i+1:]...)
			return true
		}
	}
	return false
}

// RemoveUncommitted 摘除事务 tx 的所有未提交版本（回滚/崩溃恢复用），
// 返回摘除数量。已提交版本不受影响。
func (c *Chain) RemoveUncommitted(tx txid.T) int {
	kept := c.vs[:0]
	n := 0
	for _, v := range c.vs {
		if !v.Live && v.Tx == tx {
			n++
			continue
		}
		kept = append(kept, v)
	}
	c.vs = kept
	return n
}

// Package version 维护单个键的版本链。
//
// 版本链只保存已提交版本，按提交事务号升序排列。
// 可见性判定通过注入的 View 接口完成，本包不认识 snapshot 包。
package version

import "ontology/txid"

// Version 是一个已提交版本。Deleted 为真表示删除标记版本。
type Version struct {
	Commit  txid.ID // 提交该版本的事务号
	Value   []byte  // 值；删除标记版本无意义
	Deleted bool    // 是否为删除标记
}

// View 是可见性判定视图，由 snapshot 包的快照实现。
//
// 版本 v 对视图可见，当且仅当：
//
//	v.Commit < View.Point()  且  该提交事务不在视图的活跃集合里。
type View interface {
	// Point 返回快照点（左闭右开的右边界，取不到）。
	Point() txid.ID
	// IsActive 报告提交事务号 id 是否在快照建立时仍未提交。
	IsActive(id txid.ID) bool
}

// Chain 是单个键的已提交版本链，按提交事务号升序。
type Chain struct {
	committed []*Version
}

// NewChain 返回空版本链。
func NewChain() *Chain { return &Chain{} }

// Append 按提交事务号有序插入一个已提交版本。
// 提交事务号在事务开始时分配，提交顺序可能与事务号顺序不同，故需有序插入。
func (c *Chain) Append(v *Version) {
	i := len(c.committed)
	for i > 0 && c.committed[i-1].Commit.After(v.Commit) {
		i--
	}
	c.committed = append(c.committed, nil)
	copy(c.committed[i+1:], c.committed[i:])
	c.committed[i] = v
}

// VisibleAt 返回视图可见的最新版本；第二个返回值报告是否存在可见版本。
func (c *Chain) VisibleAt(view View) (*Version, bool) {
	for i := len(c.committed) - 1; i >= 0; i-- {
		v := c.committed[i]
		if v.Commit.Before(view.Point()) && !view.IsActive(v.Commit) {
			return v, true
		}
	}
	return nil, false
}

// Newest 返回链上最新的已提交版本。
func (c *Chain) Newest() (*Version, bool) {
	if len(c.committed) == 0 {
		return nil, false
	}
	return c.committed[len(c.committed)-1], true
}

// Len 返回已提交版本数。
func (c *Chain) Len() int { return len(c.committed) }

// Remove 回收指定提交事务号的版本。
//
// 安全约束：只允许移除被更新的已提交版本遮蔽的旧版本，
// 链上最新版本永不可通过此路径移除。不存在时返回 false。
func (c *Chain) Remove(commit txid.ID) bool {
	for i, v := range c.committed {
		if v.Commit == commit {
			if i == len(c.committed)-1 {
				return false // 最新版本不可回收
			}
			c.committed = append(c.committed[:i], c.committed[i+1:]...)
			return true
		}
	}
	return false
}

// RemoveUncommitted 在崩溃恢复/回滚路径上强制移除指定版本，
// 即使它是链上最新版本。仅供恢复逻辑使用。
func (c *Chain) RemoveUncommitted(commit txid.ID) bool {
	for i, v := range c.committed {
		if v.Commit == commit {
			c.committed = append(c.committed[:i], c.committed[i+1:]...)
			return true
		}
	}
	return false
}

// Committed 返回按提交事务号升序的全部已提交版本（只读快照）。
func (c *Chain) Committed() []*Version {
	out := make([]*Version, len(c.committed))
	copy(out, c.committed)
	return out
}

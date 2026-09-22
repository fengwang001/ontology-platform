// Package version 管理单个键的版本链。
//
// 版本只能追加（append-only），链头是最新版本。链中只存在“已提交”的版本：
// 未提交与已回滚的版本从不进入链，因此回滚不会留下任何残留。
// 可见性判定不在本包内完成：调用方传入基于读快照的谓词，从而保证依赖方向
// （version 只依赖 txid，不依赖 snapshot）。
package version

import (
	"errors"
	"sync"

	"ontology/txid"
)

// ErrChainTooLong 表示单键版本链长度达到上限，追加在任何状态变更前被拒绝。
var ErrChainTooLong = errors.New("version: version chain length limit exceeded")

// Entry 是版本链上的一个不可变版本。
type Entry struct {
	commit  txid.TxID // 提交该版本的事务号
	deleted bool      // 是否为删除标记版本
	value   []byte
	older   *Entry
}

// CommitTx 返回提交事务号。
func (e Entry) CommitTx() txid.TxID { return e.commit }

// Deleted 报告该版本是否为删除标记。
func (e Entry) Deleted() bool { return e.deleted }

// Value 返回版本值的副本；删除标记版本返回 nil。
func (e Entry) Value() []byte {
	if e.deleted || len(e.value) == 0 {
		return nil
	}
	out := make([]byte, len(e.value))
	copy(out, e.value)
	return out
}

// Chain 是单个键的版本链，零值不可用，请用 NewChain 构造。
type Chain struct {
	mu     sync.Mutex
	head   *Entry
	length int
	maxLen int
}

// NewChain 创建一条上限为 maxLen（<=0 表示不限）的版本链。
func NewChain(maxLen int) *Chain { return &Chain{maxLen: maxLen} }

// Len 返回当前链上版本数。
func (c *Chain) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.length
}

// Append 在链头追加一个已提交版本。
// 超限时在任何修改发生前返回 ErrChainTooLong。
func (c *Chain) Append(commit txid.TxID, value []byte, deleted bool) error {
	if !commit.IsValid() {
		return errors.New("version: commit txid must be valid")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.maxLen > 0 && c.length >= c.maxLen {
		return ErrChainTooLong
	}
	e := &Entry{commit: commit, deleted: deleted, older: c.head}
	if !deleted && len(value) > 0 {
		e.value = make([]byte, len(value))
		copy(e.value, value) // 防御性拷贝，调用方事后修改切片不影响存储
	}
	c.head = e
	c.length++
	return nil
}

// Find 从链头（最新）向链尾（最旧）查找第一个满足以下任一条件的版本：
//   - 该版本由 self 事务写入（读己之写，self 为零时忽略）；
//   - visible(commitTx) 为真（基于读快照的已提交可见性）。
//
// 找不到时 ok 为 false，包括“从未存在”和“可见的是删除标记”两种情况，
// 后者由返回 Entry 的 Deleted 区分。
func (c *Chain) Find(visible func(txid.TxID) bool, self txid.TxID) (Entry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for e := c.head; e != nil; e = e.older {
		if self.IsValid() && e.commit == self {
			return *e, true
		}
		if visible(e.commit) {
			return *e, true
		}
	}
	return Entry{}, false
}

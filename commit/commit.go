// Package commit 实现事务归并、快照、互斥校验+提交与自动重试。依赖 cell。
package commit

import (
	"errors"
	"maps"
	"slices"
	"sync"

	"ontology/cell"
)

// 可判定的哨兵错误。
var (
	ErrEmptyBatch = errors.New("commit: empty batch")
	ErrEmptyKey   = errors.New("commit: empty key")
	ErrZeroDelta  = errors.New("commit: zero delta")
	ErrBadConfig  = errors.New("commit: maxRetries < 1")
	ErrContended  = errors.New("commit: retries exhausted")
)

// Op 是一条写操作：对 Key 应用 Delta（Delta 非零）。
type Op struct {
	Key   string
	Delta int64
}

// Hook 若非空，在 "snapshot"/"conflict"/"commit" 边界被调用，供演示与测试
// 做确定性交错；须在任何 Commit 前设置，并发期不得修改。
var Hook func(phase string, keys []string)

func hook(phase string, keys []string) {
	if Hook != nil {
		Hook(phase, keys)
	}
}

// Committer 是物化视图的乐观并发提交器。
type Committer struct {
	mu         sync.Mutex
	cells      map[string]cell.Cell
	maxRetries int
	retries    int64
	lastChecks int // 最近一次成功提交校验阶段检查过的 Key 个数（非导出）
}

// New 构造提交器；maxRetries 必须 >= 1，否则 ErrBadConfig。
func New(maxRetries int) (*Committer, error) {
	if maxRetries < 1 {
		return nil, ErrBadConfig
	}
	return &Committer{cells: make(map[string]cell.Cell), maxRetries: maxRetries}, nil
}

// merge 校验并归并：按 Key 求净增量、去掉净增量为 0 的 Key，返回字典序
// Key 列表；非法输入在此整体拒绝，不触碰任何状态。
func merge(batch []Op) (map[string]int64, []string, error) {
	if len(batch) == 0 {
		return nil, nil, ErrEmptyBatch
	}
	net := make(map[string]int64, len(batch))
	for _, op := range batch {
		if op.Key == "" {
			return nil, nil, ErrEmptyKey
		}
		if op.Delta == 0 {
			return nil, nil, ErrZeroDelta
		}
		net[op.Key] += op.Delta
	}
	for k, d := range net {
		if d == 0 {
			delete(net, k)
		}
	}
	if len(net) == 0 {
		return nil, nil, ErrEmptyBatch
	}
	return net, slices.Sorted(maps.Keys(net)), nil
}

// snapshot 按 keys（已排序）读当前 (val, ver)；未出现过的 Key 读作零值。
func (c *Committer) snapshot(keys []string) []cell.Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	snaps := make([]cell.Snapshot, len(keys))
	for i, k := range keys {
		snaps[i] = c.cells[k].Snapshot()
	}
	return snaps
}

// tryCommit 锁内原子「校验+提交」：任一 Key 版本变了则不改动返回 false；
// 全部未变才统一应用。retried 时成功把 retries +1。
func (c *Committer) tryCommit(keys []string, net map[string]int64, snaps []cell.Snapshot, retried bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, k := range keys {
		if !c.cells[k].Unchanged(snaps[i]) {
			return false
		}
	}
	for _, k := range keys {
		cl := c.cells[k]
		cl.Apply(net[k])
		c.cells[k] = cl
	}
	c.lastChecks = len(keys)
	if retried {
		c.retries++
	}
	return true
}

// Commit 归并后反复「快照->校验+提交」，至多 maxRetries 次；耗尽返回 ErrContended。
func (c *Committer) Commit(batch []Op) error {
	net, keys, err := merge(batch)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < c.maxRetries; attempt++ {
		snaps := c.snapshot(keys)
		hook("snapshot", keys)
		if c.tryCommit(keys, net, snaps, attempt > 0) {
			hook("commit", keys)
			return nil
		}
		hook("conflict", keys)
	}
	return ErrContended
}

// View 返回当前 Key -> val 快照。
func (c *Committer) View() map[string]int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]int64, len(c.cells))
	for k, cl := range c.cells {
		out[k] = cl.Val()
	}
	return out
}

// Retries 返回累计「冲突后重试并最终成功」的次数。
func (c *Committer) Retries() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.retries
}

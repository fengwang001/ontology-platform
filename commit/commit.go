// Package commit 实现事务归并、快照、互斥校验+提交与自动重试。
package commit

import (
	"errors"
	"sort"
	"sync"
	"sync/atomic"

	"ontology/cell"
)

// 可判定的哨兵错误，互不相同。
var (
	ErrEmptyBatch = errors.New("commit: empty batch")
	ErrEmptyKey   = errors.New("commit: empty key")
	ErrZeroDelta  = errors.New("commit: zero delta")
	ErrBadConfig  = errors.New("commit: maxRetries < 1")
	ErrContended  = errors.New("commit: retries exhausted")
)

// Op 是一条操作：对 Key 施加非零增量 Delta。
type Op struct {
	Key   string
	Delta int64
}

// merge 校验并归并 batch：去重求净增量，剔除净增量为 0 的 Key。纯函数，拒绝不留痕。
func merge(batch []Op) (map[string]int64, error) {
	net := make(map[string]int64, len(batch))
	for _, op := range batch {
		if op.Key == "" {
			return nil, ErrEmptyKey
		}
		if op.Delta == 0 {
			return nil, ErrZeroDelta
		}
		net[op.Key] += op.Delta
	}
	for k, d := range net {
		if d == 0 {
			delete(net, k)
		}
	}
	if len(net) == 0 {
		return nil, ErrEmptyBatch
	}
	return net, nil
}

// Engine 是乐观并发提交器：进程内存状态 + 单互斥锁。
type Engine struct {
	mu          sync.Mutex
	cells       map[string]cell.Cell
	maxRetries  int
	retries     atomic.Int64 // 累计「冲突后重试并最终成功」的次数
	lastChecked int          // 最近一次成功提交校验阶段检查的 Key 个数（非导出）
}

// New 创建 Engine；maxRetries 必须 ≥ 1，否则 ErrBadConfig。
func New(maxRetries int) (*Engine, error) {
	if maxRetries < 1 {
		return nil, ErrBadConfig
	}
	return &Engine{cells: make(map[string]cell.Cell), maxRetries: maxRetries}, nil
}

// snapshot 是一次尝试的第 3 步：按字典序读取去重后各 Key 的 (val, ver)。
type snapshot struct {
	keys  []string
	snaps []cell.Snap
	net   map[string]int64
}

func (e *Engine) snapshotOf(net map[string]int64) snapshot {
	keys := make([]string, 0, len(net))
	for k := range net {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	s := snapshot{keys, make([]cell.Snap, len(keys)), net}
	e.mu.Lock()
	for i, k := range keys {
		s.snaps[i] = e.cells[k].Snapshot()
	}
	e.mu.Unlock()
	return s
}

// tryCommit 是第 4 步：锁内整体原子校验+提交；任一 Key 版本变了则不做任何改动。
func (e *Engine) tryCommit(s snapshot) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i, k := range s.keys {
		if !e.cells[k].Unchanged(s.snaps[i]) {
			return false
		}
	}
	for _, k := range s.keys {
		e.cells[k] = e.cells[k].Apply(s.net[k])
	}
	e.lastChecked = len(s.keys)
	return true
}

// Commit 提交事务，冲突自动重试，至多 maxRetries 次尝试；耗尽返回 ErrContended。
func (e *Engine) Commit(batch []Op) error {
	net, err := merge(batch)
	if err != nil {
		return err
	}
	conflicts := 0
	for attempt := 0; attempt < e.maxRetries; attempt++ {
		if e.tryCommit(e.snapshotOf(net)) {
			e.retries.Add(int64(conflicts)) // 只有最终成功才计入重试
			return nil
		}
		conflicts++
	}
	return ErrContended // 不留任何痕迹
}

// View 返回当前 Key → val 快照。
func (e *Engine) View() map[string]int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make(map[string]int64, len(e.cells))
	for k, c := range e.cells {
		out[k] = c.Val
	}
	return out
}

// Retries 返回累计的「冲突后重试并最终成功」的次数。
func (e *Engine) Retries() int64 { return e.retries.Load() }

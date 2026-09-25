// Package acc 实现多 Key 的累加器：持久化的 sum 与 cp、易失的 pending、
// Commit 连续前缀折叠与 Restore 崩溃模拟。依赖 ckpt 包。
package acc

import (
	"errors"
	"sync"

	"ontology/ckpt"
)

// 三类可判定故障，互不相同。
var (
	ErrEmptyKey       = errors.New("acc: empty key")
	ErrNegativeOffset = errors.New("acc: negative offset")
	ErrTooManyPending = errors.New("acc: pending exceeds maxPending")
)

type rec struct {
	key   string
	delta int64
}

// Acc 是断点续传累加器。sum 与 cp 为持久化状态，pending 为易失状态。
type Acc struct {
	mu         sync.Mutex
	maxPending int
	sum        map[string]int64 // 持久化
	cp         int64            // 持久化：已提交位点
	pending    map[int64]rec    // 易失：已 Apply 未 Commit
	lastChecks int              // 非导出：最近一次 Apply 判重检查过的 pending 条目数
}

// New 创建累加器，maxPending 为在途条目上限，必须为正。
func New(maxPending int) (*Acc, error) {
	if maxPending <= 0 {
		return nil, errors.New("acc: maxPending must be positive")
	}
	return &Acc{
		maxPending: maxPending,
		sum:        map[string]int64{},
		cp:         -1,
		pending:    map[int64]rec{},
	}, nil
}

// Apply 投递一条记录。重复（已持久化或在途）幂等跳过；
// 空 Key、负 offset、在途超限整体失败且不留痕。
func (a *Acc) Apply(key string, offset, delta int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lastChecks = 0
	if key == "" {
		return ErrEmptyKey
	}
	if offset < 0 {
		return ErrNegativeOffset
	}
	a.lastChecks++ // 一次映射查找即完成在途判重，不随 pending 规模增长
	d := ckpt.Decide(offset, a.cp, has(a.pending, offset))
	if d != ckpt.Accept {
		return nil // 幂等跳过，不改任何状态
	}
	if len(a.pending) >= a.maxPending {
		return ErrTooManyPending
	}
	a.pending[offset] = rec{key: key, delta: delta}
	return nil
}

func has(m map[int64]rec, offset int64) bool {
	_, ok := m[offset]
	return ok
}

// Commit 从 cp+1 起折叠连续前缀进 sum 并推进 cp，遇第一个缺口即停。
func (a *Acc) Commit() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cp = ckpt.Fold(a.cp, func(offset int64) bool {
		r, ok := a.pending[offset]
		if !ok {
			return false
		}
		a.sum[r.key] += r.delta
		delete(a.pending, offset)
		return true
	})
}

// Restore 模拟崩溃重启：清空易失的 pending，sum 与 cp 保持不变。
func (a *Acc) Restore() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pending = map[int64]rec{}
}

// Checkpoint 返回已提交位点 cp。
func (a *Acc) Checkpoint() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cp
}

// Sum 返回 key 的已提交累加值。
func (a *Acc) Sum(key string) int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sum[key]
}

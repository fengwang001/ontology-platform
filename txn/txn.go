// Package txn 实现「先效果后位点」的两阶段提交状态机。
// 依赖 eff；不依赖 api。
package txn

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"ontology/eff"
)

// 可判定哨兵错误，互不相同。
var (
	ErrInvalidSeq    = errors.New("txn: invalid seq (<= 0)")
	ErrOutOfOrder    = errors.New("txn: apply out of order (seq > C+1)")
	ErrEffectMissing = errors.New("txn: effect missing for C+1")
	ErrOffsetJump    = errors.New("txn: offset jump (seq > C+1)")
)

// Txn 是两阶段提交器。C 含义：下一条要处理的序号（seq <= C 均已提交）。
type Txn struct {
	mu      sync.RWMutex
	store   *eff.Store         // 已持久化的副作用（重启保留）
	c       int64              // 已提交位点（重启保留）
	pending map[int64]struct{} // 易失缓存：已写效果但未提交的序号（重启丢弃重建）

	lastChecked int // 非导出：最近一次推进 C 的 Commit 检查过的 store 条目数
}

// New 返回 C=0、store 空的提交器。
func New() *Txn {
	return &Txn{store: eff.New(), pending: make(map[int64]struct{})}
}

// Apply 阶段一（先效果）：幂等覆盖写 store[seq] = eff，不推进 C。
func (t *Txn) Apply(seq, effv int64) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if seq <= 0 {
		return ErrInvalidSeq
	}
	switch {
	case seq <= t.c:
		return nil // 已提交，幂等成功
	case seq > t.c+1:
		return ErrOutOfOrder // 拒绝：不改任何状态
	}
	t.store.Put(seq, effv)
	t.pending[seq] = struct{}{}
	return nil
}

// Commit 阶段二（后位点）：仅当 store[C+1] 已存在时把 C 推进到 seq。
func (t *Txn) Commit(seq int64) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if seq <= 0 {
		return ErrInvalidSeq
	}
	switch {
	case seq <= t.c:
		return nil // 幂等成功
	case seq > t.c+1:
		return ErrOffsetJump // 位点跳跃：C+1..seq-1 的效果被跳过
	}
	// seq == C+1：连续性检查只查这一处，O(1)。
	checked := 0
	_, ok := t.store.Get(seq)
	checked++
	if !ok {
		return ErrEffectMissing // 先效果后位点被违反
	}
	t.c = seq
	delete(t.pending, seq)
	t.lastChecked = checked
	return nil
}

// Committed 返回当前已提交位点 C。
func (t *Txn) Committed() int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.c
}

// Pending 返回已写副作用但位点未提交的序号（升序）。
func (t *Txn) Pending() []int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.pendingLocked()
}

func (t *Txn) pendingLocked() []int64 {
	out := make([]int64, 0, len(t.pending))
	for s := range t.pending {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Restart 模拟崩溃重启：保留已持久化的 store 与 C，丢弃易失的
// pending 缓存并从 store 重建；返回重建后的 Pending（消费端据此重复处理）。
// 若发现已提交前缀缺效果（持久化损坏），返回错误。
func (t *Txn) Restart() ([]int64, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pending = make(map[int64]struct{}) // 丢弃易失结构
	snap := t.store.Snapshot()
	for seq := range snap {
		if seq > t.c {
			t.pending[seq] = struct{}{}
		}
	}
	for seq := int64(1); seq <= t.c; seq++ { // 重建时校验不丢不变量
		if _, ok := snap[seq]; !ok {
			return nil, fmt.Errorf("txn: committed effect %d lost: %w", seq, ErrEffectMissing)
		}
	}
	return t.pendingLocked(), nil
}

// Snapshot 返回 store 副本（供测试对照朴素参照）。
func (t *Txn) Snapshot() map[int64]int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.store.Snapshot()
}

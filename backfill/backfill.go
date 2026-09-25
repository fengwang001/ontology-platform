// Package backfill 实现回填/在线双流的阶段判定、边界 W、
// 软切换状态与按 Key 去重计数聚合。依赖 dedup。
package backfill

import (
	"errors"
	"sync"

	"ontology/dedup"
)

// 可判定的哨兵错误，互不相同。
var (
	ErrNegativeW       = errors.New("backfill: waterline W 为负")
	ErrSeqOutOfRange   = errors.New("backfill: 事件 Seq >= W，越界")
	ErrBackfillClosed  = errors.New("backfill: 回填已完成，拒绝再回填")
	ErrEmptyKey        = errors.New("backfill: 事件 Key 为空串")
	errSelfCheckFailed = errors.New("backfill: 自检不变量被破坏")
)

// Event 是一条变更事件：Seq 全局唯一，Key 出现一次计 +1。
type Event struct {
	Seq int64
	Key string
}

// Engine 维护「每个 Key 的去重事件计数」物化视图。
type Engine struct {
	mu     sync.RWMutex
	w      int64
	done   bool // 回填是否已完成（软切换闸门）
	set    *dedup.Set
	counts map[string]int64
}

// New 创建引擎；W 为负返回 ErrNegativeW。
func New(w int64) (*Engine, error) {
	if w < 0 {
		return nil, ErrNegativeW
	}
	return &Engine{w: w, set: dedup.New(), counts: make(map[string]int64)}, nil
}

// Backfill 应用一批历史事件，要求全部 Seq < W 且回填未完成；
// 任一条非法则整批拒绝、状态零改变。
func (e *Engine) Backfill(evs []Event) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.done {
		return ErrBackfillClosed
	}
	for _, ev := range evs {
		if ev.Key == "" {
			return ErrEmptyKey
		}
		if ev.Seq >= e.w {
			return ErrSeqOutOfRange
		}
	}
	e.apply(evs)
	return nil
}

// Online 应用一批在线事件，Seq 任意；空 Key 整批拒绝。
func (e *Engine) Online(evs []Event) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, ev := range evs {
		if ev.Key == "" {
			return ErrEmptyKey
		}
	}
	e.apply(evs)
	return nil
}

// apply 逐条去重应用；调用方须持写锁且已完成整批校验。
// 只有 dedup.Add 成功（首次到达）才累加计数——不变量 1、3 的落点。
func (e *Engine) apply(evs []Event) {
	for _, ev := range evs {
		if e.set.Add(ev.Seq) {
			e.counts[ev.Key]++
		}
	}
}

// Complete 标记回填完成；只置闸门，不触碰任何计数——不变量 2 的落点。
func (e *Engine) Complete() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.done = true
	return nil
}

// View 返回每个 Key 的去重计数快照。
func (e *Engine) View() map[string]int64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make(map[string]int64, len(e.counts))
	for k, v := range e.counts {
		out[k] = v
	}
	return out
}

// Seen 返回已应用的不同 Seq 总数。
func (e *Engine) Seen() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.set.Len()
}

// SelfCheck 核验不变量：各 Key 计数之和必须等于已应用 Seq 总数，
// 且计数不得为负。
func (e *Engine) SelfCheck() error {
	e.mu.RLock()
	defer e.mu.RUnlock()
	var sum int64
	for _, v := range e.counts {
		if v < 0 {
			return errSelfCheckFailed
		}
		sum += v
	}
	if sum != int64(e.set.Len()) {
		return errSelfCheckFailed
	}
	return nil
}

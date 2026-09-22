// Package query 在 tree 之上提供点查、区间重叠查询、批量查询与确定性排序。
// 仅依赖 tree 与 ival。
package query

import (
	"sync/atomic"

	"ontology/ival"
	"ontology/tree"
)

// DefaultMaxBatch 是单次批量查询的默认区间数上限。
const DefaultMaxBatch = 10_000

// Engine 包装一棵树并提供查询服务，可被多 goroutine 并发使用。
type Engine struct {
	tree *tree.Tree

	// visits 是最近一次查询实际访问的树节点数；非导出，不属于公开接口。
	// 每次查询结束时整体替换，查询期间不产生共享写。
	visits   atomic.Uint64
	maxBatch int
}

// Option 配置 Engine。
type Option func(*Engine)

// WithMaxBatch 设置单次批量查询区间数上限；n<=0 表示不限制。
func WithMaxBatch(n int) Option {
	return func(e *Engine) { e.maxBatch = n }
}

// New 创建查询引擎。
func New(t *tree.Tree, opts ...Option) *Engine {
	e := &Engine{tree: t, maxBatch: DefaultMaxBatch}
	for _, o := range opts {
		o(e)
	}
	return e
}

// Stab 点查：返回包含 x 的区间，按 (L,R) 稳定升序。
func (e *Engine) Stab(x int64) []ival.Interval {
	var v uint64
	out := e.tree.Stab(x, func() { v++ })
	e.visits.Store(v)
	return out
}

// Overlap 返回与 q 交点集非空的区间，按 (L,R) 稳定升序。
// 非法区间返回 ival.ErrInvalidInterval；零长度 q 合法但结果恒为空。
func (e *Engine) Overlap(q ival.Interval) ([]ival.Interval, error) {
	if q.L > q.R {
		return nil, ival.ErrInvalidInterval
	}
	var v uint64
	out := e.tree.Overlap(q, func() { v++ })
	e.visits.Store(v)
	return out, nil
}

// BatchOverlap 一次执行多个重叠查询，返回与 qs 等长、逐元素对应的结果。
// 超过批量上限时立刻拒绝，不执行其中任何一次查询，无部分执行。
func (e *Engine) BatchOverlap(qs []ival.Interval) ([][]ival.Interval, error) {
	if e.maxBatch > 0 && len(qs) > e.maxBatch {
		return nil, &BatchLimitError{Limit: e.maxBatch}
	}
	results := make([][]ival.Interval, len(qs))
	for i, q := range qs {
		if q.L > q.R {
			return nil, ival.ErrInvalidInterval
		}
		var v uint64
		results[i] = e.tree.Overlap(q, func() { v++ })
		e.visits.Store(v)
	}
	return results, nil
}

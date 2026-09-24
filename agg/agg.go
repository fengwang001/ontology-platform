// Package agg 在给定分组集上做 SUM(M) 的增量维护：只按投影键 O(1) 定位，
// 绝不扫描现存键；撤回会使任一组变负时整体拒绝；归零条目立即删除。
// 它只依赖 gset。
package agg

import (
	"errors"
	"sync"

	"ontology/gset"
)

// Fact 是三维事实流的一条记录；M>0 累加，M<0 撤回。
type Fact struct {
	A, B, C string
	M       int64
}

// 哨兵错误：可被 errors.Is 判定，四者互不相同。
var (
	// ErrEmptyDimension：事实的某个维度取值为空串。
	ErrEmptyDimension = errors.New("agg: dimension value must not be empty")
	// ErrZeroMeasure：M == 0，既非累加也非撤回。
	ErrZeroMeasure = errors.New("agg: measure M must not be zero")
	// ErrNegativeResult：撤回会使某个 (组,键) 的 sum 变成负数。
	ErrNegativeResult = errors.New("agg: withdrawal would make a sum negative")
)

// Engine 是分组集求和物化视图的增量引擎。
type Engine struct {
	mu sync.RWMutex

	// groups 即 New 时指定的分组集，顺序稳定，不多不少。
	groups []gset.Group
	// tables[组ID][投影键] = 当前 sum；归零的键已被 delete。
	tables map[int]map[string]int64

	// probes 是非导出计数器：最近一次 Apply 为定位受影响 (组,键)
	// 而检查过的现存键个数。每组恰好一次哈希探测，故与现存键总数无关。
	probes int
}

// New 用已经构造好的分组集创建引擎（分组集本身的合法性由上层保证）。
func New(groups []gset.Group) *Engine {
	e := &Engine{
		groups: make([]gset.Group, len(groups)),
		tables: make(map[int]map[string]int64, len(groups)),
	}
	copy(e.groups, groups)
	for _, g := range e.groups {
		e.tables[g.ID()] = map[string]int64{}
	}
	return e
}

// Apply 增量施加一条事实。先对所有分组集完成预检，全部通过后才统一提交，
// 因此被拒时状态逐字节不变。
func (e *Engine) Apply(f Fact) error {
	if f.A == "" || f.B == "" || f.C == "" {
		return ErrEmptyDimension
	}
	if f.M == 0 {
		return ErrZeroMeasure
	}
	vals := [gset.DimCount]string{f.A, f.B, f.C}

	e.mu.Lock()
	defer e.mu.Unlock()

	e.probes = 0
	keys := make([]string, len(e.groups))
	curs := make([]int64, len(e.groups))
	for i, g := range e.groups {
		k := g.Key(vals)
		keys[i] = k
		curs[i] = e.tables[g.ID()][k] // 一次哈希探测即定位，不扫描全集。
		e.probes++
		if curs[i]+f.M < 0 {
			return ErrNegativeResult // 尚未发生任何写入，整体失败。
		}
	}
	for i, g := range e.groups {
		t := e.tables[g.ID()]
		sum := curs[i] + f.M
		if sum == 0 {
			delete(t, keys[i]) // 零值移除：绝不物化零值条目。
		} else {
			t[keys[i]] = sum
		}
	}
	return nil
}

// Snapshot 返回视图的深拷贝（组 ID → 键串 → sum），调用方可自由修改。
func (e *Engine) Snapshot() map[int]map[string]int64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make(map[int]map[string]int64, len(e.tables))
	for id, t := range e.tables {
		cp := make(map[string]int64, len(t))
		for k, v := range t {
			cp[k] = v
		}
		out[id] = cp
	}
	return out
}

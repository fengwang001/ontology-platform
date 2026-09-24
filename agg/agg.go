// Package agg 在给定分组集上做增量 SUM 维护：按 (组,键) 记账、
// Apply 增量累加、归零即移除、撤回越界整体拒绝。依赖 gset。
package agg

import (
	"errors"
	"fmt"
	"strconv"

	"ontology/gset"
)

// ErrWithdrawn 表示撤回会使某个 (组,键) 的 sum 变为负数；
// 此时 Apply 不做任何写入。
var ErrWithdrawn = errors.New("agg: withdrawal would make a group sum negative")

// Table 是指定分组集上的求和物化视图（进程内存、无锁；
// 并发由上层 api 串行化）。
type Table struct {
	groups []gset.Group
	sums   map[int]map[string]int64 // 组 ID → 投影键串 → sum（零值键不存在）

	// probe 记录最近一次 Apply 为定位受影响 (组,键) 而检查过的
	// 现存键个数：每组只做一次 map 查找，命中算 1、未命中算 0。
	// 非导出，仅供同包内部测试读取。
	probe int
}

// New 按指定分组集建空表。groups 由调用方保证合法。
func New(groups []gset.Group) *Table {
	t := &Table{
		groups: append([]gset.Group(nil), groups...),
		sums:   make(map[int]map[string]int64, len(groups)),
	}
	for _, g := range t.groups {
		t.sums[g.ID()] = map[string]int64{}
	}
	return t
}

// Apply 把 m 增量累加到每个分组集的投影键上。m<0 为撤回：
// 先对所有组预检 newSum>=0，全部通过才提交，任一越界则状态不变。
func (t *Table) Apply(v [gset.NumDims]string, m int64) error {
	type hit struct {
		id  int
		key string
		old int64
	}
	hits := make([]hit, 0, len(t.groups))
	t.probe = 0
	for _, g := range t.groups {
		key := g.Key(v)
		bucket := t.sums[g.ID()]
		old, ok := bucket[key] // O(1) 哈希定位，不扫描现存键全集
		if ok {
			t.probe++ // 检查到 1 个现存键
		}
		if old+m < 0 {
			return ErrWithdrawn // 预检阶段：尚未发生任何写入
		}
		hits = append(hits, hit{id: g.ID(), key: key, old: old})
	}
	for _, h := range hits {
		next := h.old + m
		if next == 0 {
			delete(t.sums[h.id], h.key) // 零值移除，不物化零
			continue
		}
		t.sums[h.id][h.key] = next
	}
	return nil
}

// Snapshot 返回视图的深拷贝（组 ID → 键串 → sum），调用方可自由修改。
func (t *Table) Snapshot() map[int]map[string]int64 {
	out := make(map[int]map[string]int64, len(t.sums))
	for id, bucket := range t.sums {
		cp := make(map[string]int64, len(bucket))
		for k, s := range bucket {
			cp[k] = s
		}
		out[id] = cp
	}
	return out
}

// CheckProbeConstant 验证定位复杂度：先在组 (A) 预置 m 个互异键，再
// Apply 一条命中其中某已有键的事实。检查过的现存键数必须恒为 1
// （每组恰好一次哈希命中），不随 m 线性增长。只返回成败，不暴露
// probe 的数值（数值仅供同包内部测试直接读字段）。
func CheckProbeConstant(sizes ...int) error {
	g, _ := gset.NewGroup([]int{0})
	for _, m := range sizes {
		t := New([]gset.Group{g})
		for i := 0; i < m; i++ {
			v := [gset.NumDims]string{"k" + strconv.Itoa(i), "b", "c"}
			if err := t.Apply(v, 1); err != nil {
				return err
			}
		}
		if err := t.Apply([gset.NumDims]string{"k0", "b", "c"}, 1); err != nil {
			return err
		}
		if t.probe != 1 { // 命中 1 个现存键即定位完成，与 m 无关
			return fmt.Errorf("agg: probe count grew with m=%d", m)
		}
	}
	return nil
}

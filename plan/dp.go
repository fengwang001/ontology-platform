package plan

import (
	"errors"
	"fmt"
	"math/bits"
	"sort"

	"ontology/catalog"
	"ontology/cost"
)

// MaxTables 是 DP 中间表数量的硬上限（状态数 2^MaxTables）。
const MaxTables = 16

var (
	// ErrTooManyTables 表示表数量超过硬上限，在枚举前拒绝。
	ErrTooManyTables = errors.New("plan: too many tables")
	// ErrNoTables 表示空查询。
	ErrNoTables = errors.New("plan: no tables")
)

// Result 是一次计划选择的结果；计数器非导出，经方法读取。
type Result struct {
	Best       *Plan
	subsets    uint64
	partitions uint64
}

// Subsets 返回考察过的子集数。
func (r *Result) Subsets() uint64 { return r.subsets }

// Partitions 返回考察过的划分数。
func (r *Result) Partitions() uint64 { return r.partitions }

// Select 对只读视图 v 做按子集的 DP 枚举，返回最低代价计划。
// 不持有任何可变共享状态，可并发调用。
func Select(v *catalog.View) (*Result, error) {
	n := len(v.Tables)
	if n == 0 {
		return nil, ErrNoTables
	}
	if n > MaxTables {
		return nil, fmt.Errorf("%w: %d > %d", ErrTooManyTables, n, MaxTables)
	}
	// 内部一律按表名排序建索引：最低位即名字最小的表，
	// 使规范划分的考察集合与登记顺序无关（可复现性，见 DESIGN.md 第 2 节）。
	tables := make([]catalog.TableInfo, n)
	copy(tables, v.Tables)
	sort.Slice(tables, func(i, j int) bool { return tables[i].Name < tables[j].Name })
	idx := make(map[string]int, n)
	for i, t := range tables {
		idx[t.Name] = i
	}
	best := make([]*Plan, 1<<n)
	res := &Result{}
	for mask := 1; mask < 1<<n; mask++ {
		res.subsets++
		if mask&(mask-1) == 0 {
			t := tables[bits.TrailingZeros(uint(mask))]
			best[mask] = newScan(t.Name, t.Rows)
			continue
		}
		connected := components(mask, n, idx, v.Preds) == 1
		low := mask & (-mask)
		for s := (mask - 1) & mask; s > 0; s = (s - 1) & mask {
			if s&low == 0 {
				continue // 每个划分只考察一次（最低位恒在左侧）
			}
			res.partitions++
			other := mask ^ s
			cross, unreliable := crossing(s, other, idx, v.Preds)
			if connected != (len(cross) > 0) {
				continue // 连通子集只用谓词连接；不连通子集只用分量间笛卡尔积
			}
			l, r := best[s], best[other]
			sels := make([]float64, len(cross))
			for i, p := range cross {
				sels[i] = p.Sel
			}
			card := cost.JoinCard(l.Card, r.Card, sels...)
			total := cost.TotalCost(l.Cost, r.Cost, l.Card, r.Card)
			preds := make([]catalog.Predicate, len(cross))
			for i, p := range cross {
				preds[i] = p.Pred
			}
			cand := newJoin(l, r, preds, card, total, unreliable)
			if better(cand, best[mask]) {
				best[mask] = cand
			}
		}
	}
	res.Best = best[1<<n-1]
	return res, nil
}

// components 返回谓词图在 mask 诱导子图上的连通分量数（并查集）。
func components(mask, n int, idx map[string]int, preds []catalog.PredInfo) int {
	parent := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	var find func(x int) int
	find = func(x int) int {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	in := func(name string) (int, bool) {
		i, ok := idx[name]
		return i, ok && mask>>i&1 == 1
	}
	for _, p := range preds {
		a, okA := in(p.Pred.Left.Table)
		b, okB := in(p.Pred.Right.Table)
		if okA && okB {
			parent[find(a)] = find(b)
		}
	}
	k := 0
	for i := 0; i < n; i++ {
		if mask>>i&1 == 1 && find(i) == i {
			k++
		}
	}
	return k
}

// crossing 返回所有一端在 s、另一端在 other 的谓词及其可靠性标记。
func crossing(s, other int, idx map[string]int, preds []catalog.PredInfo) ([]catalog.PredInfo, bool) {
	var out []catalog.PredInfo
	unreliable := false
	for _, p := range preds {
		li := idx[p.Pred.Left.Table]
		ri := idx[p.Pred.Right.Table]
		lInS := s>>li&1 == 1
		rInS := s>>ri&1 == 1
		lInO := other>>li&1 == 1
		rInO := other>>ri&1 == 1
		if (lInS && rInO) || (rInS && lInO) {
			out = append(out, p)
			unreliable = unreliable || p.Unreliable
		}
	}
	return out, unreliable
}

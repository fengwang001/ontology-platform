// Package compact 按 Key 折叠取最新、做窗口过滤与墓碑保留期判定。依赖 rec。
package compact

import (
	"sort"
	"sync/atomic"

	"ontology/rec"
)

// Engine 保存已 Feed 的记录并执行压缩。不是并发安全的，
// 并发控制由上层（api）负责；cmps 用原子操作以便并发 Compact 不竞态。
type Engine struct {
	recs []rec.Rec
	cmps atomic.Int64 // 最近一次 Compact 中为定位存活记录而做的 TS 比较条数（非导出）
}

// Feed 追加一条记录（调用方须已完成校验）。
func (e *Engine) Feed(r rec.Rec) {
	e.recs = append(e.recs, r)
}

// View 返回当前已 Feed 记录的副本。
func (e *Engine) View() []rec.Rec {
	out := make([]rec.Rec, len(e.recs))
	copy(out, e.recs)
	return out
}

// Compact 对窗口 [lo, hi) 做压缩：每个 Key 只保留 TS 最大的记录，
// 再按保留期 R 丢弃到期墓碑。输出按 Key 字典序升序。
// 折叠按 Key 哈希定位（map），不线性扫描任何 Key 的历史。
func (e *Engine) Compact(lo, hi, R int64) []rec.Rec {
	e.cmps.Store(0)
	latest := make(map[string]rec.Rec)
	for _, r := range e.recs {
		if r.TS < lo || r.TS >= hi {
			continue
		}
		cur, ok := latest[r.Key]
		if !ok {
			latest[r.Key] = r
			continue
		}
		e.cmps.Add(1) // 同 Key 已存在候选，比较一次 TS 决定谁存活
		if r.TS > cur.TS {
			latest[r.Key] = r
		}
	}
	out := make([]rec.Rec, 0, len(latest))
	for _, r := range latest {
		if rec.Drop(r, hi, R) {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

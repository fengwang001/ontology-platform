// Package query 在 head 之上提供瞬时值查询：取序列中 ts ≤ t 的最新样本，
// 按回看窗口 L 与陈旧标记判定 Absent/Stale/Value。
package query

import (
	"sync/atomic"

	"ontology/head"
)

// Kind 是瞬时查询的结果种类。
type Kind int

const (
	Absent Kind = iota // 序列不存在、无 ts ≤ t 的样本，或样本已超出回看窗口
	Stale              // 最新样本是陈旧标记
	Value              // 最新样本是值样本
)

func (k Kind) String() string {
	switch k {
	case Stale:
		return "Stale"
	case Value:
		return "Value"
	default:
		return "Absent"
	}
}

// Result 是一次瞬时查询的结果。
type Result struct {
	Kind Kind
	V    int64 // 仅 Kind 为 Value 时有效
}

// Querier 在某个 Head 上执行查询。
type Querier struct {
	h *head.Head
}

func New(h *head.Head) *Querier { return &Querier{h: h} }

const maxTS = int64(1e12)

// probes 统计单次 instant 考察的样本数（非导出，供测试断言二分复杂度）。
var probes int64

// Instant 查询单个序列在 t 时刻的瞬时值。
func (q *Querier) Instant(name string, t int64) Result {
	q.h.RLock()
	defer q.h.RUnlock()
	return q.instant(name, t)
}

// InstantMany 在同一时刻对多个序列取值；同一把读锁保证相对并发的
// 整次抓取写入是原子的：一次 Scrape 的全部样本要么全可见要么全不可见。
func (q *Querier) InstantMany(names []string, t int64) []Result {
	q.h.RLock()
	defer q.h.RUnlock()
	out := make([]Result, len(names))
	for i, n := range names {
		out[i] = q.instant(n, t)
	}
	return out
}

// instant 二分查找 ts ≤ t 的最后一个样本；考察样本数 ≤ ⌈log2(n+1)⌉+1。
func (q *Querier) instant(name string, t int64) Result {
	if t < 0 || t > maxTS {
		return Result{Kind: Absent}
	}
	s, ok := q.h.SeriesSamples(name)
	if !ok {
		return Result{Kind: Absent}
	}
	lo, hi := 0, len(s)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		atomic.AddInt64(&probes, 1)
		if s[mid].Ts <= t {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == 0 {
		return Result{Kind: Absent}
	}
	atomic.AddInt64(&probes, 1)
	latest := s[lo-1]
	if t-latest.Ts >= q.h.L { // 差恰等 L 不可见
		return Result{Kind: Absent}
	}
	if latest.Stale {
		return Result{Kind: Stale}
	}
	return Result{Kind: Value, V: latest.V}
}

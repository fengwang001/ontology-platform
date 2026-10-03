// Package query 在回看窗口 L 内读取序列的瞬时值。
package query

import (
	"sync/atomic"

	"ontology/head"
)

// Kind 是瞬时查询结果的类别。
type Kind int

const (
	Absent Kind = iota // 序列不存在，或窗口内无样本
	Stale              // 窗口内最新样本是陈旧标记
	Value              // 窗口内最新样本是活样本
)

// Result 是一次瞬时查询的结果；Kind 为 Value 时 V 有效。
type Result struct {
	Kind Kind
	V    int64
}

// Querier 在某个 Head 上执行瞬时查询，可并发使用。
type Querier struct {
	h *head.Head
	l int64
}

func New(h *head.Head) *Querier { return &Querier{h: h, l: h.Lookback()} }

// examined 统计二分查找考察的样本数（非导出，供测试断言上界）。
var examined atomic.Int64

// search 二分查找 samples 中 ts <= t 的最新样本。
// 命中的样本在比较时已缓存，不重复计入考察数。
func search(samples []head.Sample, t int64) (s head.Sample, found bool) {
	lo, hi := 0, len(samples)-1
	for lo <= hi {
		mid := int(uint(lo+hi) >> 1)
		examined.Add(1)
		if samples[mid].Ts <= t {
			s, found = samples[mid], true
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return s, found
}

// instantLocked 取 ts <= t 的最新样本：不存在或 t-ts >= L（恰等不可见）为
// Absent；陈旧标记为 Stale；否则为 Value。调用方须持有读锁。
func (q *Querier) instantLocked(series string, t int64) Result {
	s, found := search(q.h.SeriesSamples(series), t)
	if !found || t-s.Ts >= q.l {
		return Result{Kind: Absent}
	}
	if s.Stale {
		return Result{Kind: Stale}
	}
	return Result{Kind: Value, V: s.V}
}

// Instant 返回 series 在 t 时刻的瞬时值；序列不存在为 Absent。
func (q *Querier) Instant(series string, t int64) Result {
	q.h.RLock()
	defer q.h.RUnlock()
	return q.instantLocked(series, t)
}

// InstantMany 在同一读临界区内对多个序列在同一时刻取值，
// 相对并发的批量写入（Scrape 提交）原子：一次 Scrape 的全部样本
// 对它要么全可见，要么全不可见。
func (q *Querier) InstantMany(series []string, t int64) []Result {
	q.h.RLock()
	defer q.h.RUnlock()
	out := make([]Result, len(series))
	for i, name := range series {
		out[i] = q.instantLocked(name, t)
	}
	return out
}

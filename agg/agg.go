// Package agg 在单个时间桶内做增量聚合。
package agg

import (
	"math"
	"sort"
)

// Bucket 是一个桶的最终聚合结果。Count==0 表示空洞。
type Bucket struct {
	Start int64
	First float64
	Last  float64
	Min   float64
	Max   float64
	Mean  float64
	Count int64
}

// Accumulator 是桶内增量累加器；非并发安全，由上层 align 加锁。
// 为保证乱序输入与排序后输入逐位一致，值按到达缓存，导出 Result 时按
// IEEE 位序排序后再决定 First/Last 与求和；同值多位（同时间戳）等价。
type Accumulator struct {
	start int64
	vals  []float64
}

// New 以桶起点创建空累加器。
func New(start int64) *Accumulator { return &Accumulator{start: start} }

// Add 追加一个值。±Inf 参与累加；NaN 必须在调用前被拒绝。
func (a *Accumulator) Add(v float64) { a.vals = append(a.vals, v) }

// Count 返回已累加点数。
func (a *Accumulator) Count() int64 { return int64(len(a.vals)) }

// Result 导出聚合结果；位级结果与值进入桶的顺序无关。
func (a *Accumulator) Result() Bucket {
	n := len(a.vals)
	b := Bucket{Start: a.start, Count: int64(n)}
	if n == 0 {
		return b
	}
	ordered := append([]float64(nil), a.vals...)
	sort.Slice(ordered, func(i, j int) bool {
		return math.Float64bits(ordered[i]) < math.Float64bits(ordered[j])
	})
	b.First, b.Last = ordered[0], ordered[n-1]
	b.Min, b.Max = ordered[0], ordered[0]
	for _, v := range ordered {
		if v < b.Min {
			b.Min = v
		}
		if v > b.Max {
			b.Max = v
		}
	}
	var sum float64
	for _, v := range ordered {
		sum += v
	}
	b.Mean = sum / float64(n)
	return b
}

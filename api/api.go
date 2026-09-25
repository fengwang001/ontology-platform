// Package api 是等宽直方图的对外接口。
package api

import (
	"errors"

	"ontology/bucket"
	"ontology/hist"
)

// ErrNonPositiveWidth 表示 New 收到 width <= 0，整体失败，不产生任何半成品状态。
var ErrNonPositiveWidth = errors.New("histogram: width must be positive")

// Histogram 是并发安全的等宽直方图。
type Histogram struct{ h *hist.Hist }

// New 构造直方图；width <= 0 时返回 (nil, ErrNonPositiveWidth)。
func New(width, anchor int) (*Histogram, error) {
	if width <= 0 {
		return nil, ErrNonPositiveWidth
	}
	return &Histogram{h: hist.New(width, anchor)}, nil
}

// Insert 把 v 计入其桶，越界时扩展桶号范围。
func (g *Histogram) Insert(v int) { g.h.Insert(v) }

// BucketCount 返回桶 k 的计数（未出现的桶为 0）。
func (g *Histogram) BucketCount(k int) int { return g.h.Count(k) }

// Range 返回已分配桶号范围 [min, max]；空时 ok=false。
func (g *Histogram) Range() (min, max int, ok bool) { return g.h.Range() }

// Total 返回已 Insert 的值总数。
func (g *Histogram) Total() int { return g.h.Total() }

// SelfCheck 对内置操作序列核验四条不变量，全部通过返回 nil。
func (g *Histogram) SelfCheck() error {
	seqs := [][]int{
		{5, 12, 20, 10, -3, -15},
		{0, 7, -7, 70, -70, 3, 3, -1},
	}
	for _, seq := range seqs {
		h, err := New(10, 0)
		if err != nil {
			return err
		}
		batch := map[int]int{}
		n := 0
		for _, v := range seq {
			h.Insert(v)
			n++
			batch[bucket.Index(10, 0, v)]++
			// 不变量2：计数守恒
			if h.Total() != n {
				return errors.New("selfcheck: total not conserved")
			}
		}
		// 不变量1：与批量重算逐桶一致；不变量3：范围完整
		min, max, ok := h.Range()
		if !ok {
			return errors.New("selfcheck: range empty after inserts")
		}
		for k := min - 1; k <= max+1; k++ {
			if h.BucketCount(k) != batch[k] {
				return errors.New("selfcheck: count mismatch vs batch")
			}
		}
		if h.BucketCount(min) == 0 || h.BucketCount(max) == 0 {
			return errors.New("selfcheck: range endpoint not hit")
		}
		if h.Total() != sum(batch) {
			return errors.New("selfcheck: total mismatch")
		}
	}
	return nil
}

func sum(m map[int]int) int {
	s := 0
	for _, c := range m {
		s += c
	}
	return s
}

// Package api 是等宽直方图的对外接口，依赖 hist 包。
package api

import (
	"errors"
	"fmt"

	"ontology/bucket"
	"ontology/hist"
)

// 可判定的哨兵错误。
var (
	ErrNonPositiveWidth = errors.New("api: width must be > 0")
	ErrSelfCheck        = errors.New("api: self-check failed")
)

// Histogram 是并发可读写的等宽直方图句柄。
type Histogram struct {
	h *hist.Hist
}

// New 创建直方图。width <= 0 时整体失败：返回 nil 实例与
// ErrNonPositiveWidth，不产生任何可观察的半成品状态。
func New(width, anchor int) (*Histogram, error) {
	if width <= 0 {
		return nil, ErrNonPositiveWidth
	}
	h, err := hist.New(width, anchor)
	if err != nil {
		return nil, err
	}
	return &Histogram{h: h}, nil
}

// Insert 把值 v 计入其桶，必要时扩展已分配桶号范围。
func (g *Histogram) Insert(v int) { g.h.Insert(v) }

// BucketCount 返回桶 k 的计数，未出现的桶为 0。
func (g *Histogram) BucketCount(k int) int { return g.h.Count(k) }

// Range 返回已分配桶号范围 [minBucket, maxBucket]；空时 ok 为 false。
func (g *Histogram) Range() (min, max int, ok bool) { return g.h.Range() }

// Total 返回已 Insert 的值总数。
func (g *Histogram) Total() int { return g.h.Total() }

// SelfCheck 对内置操作序列核验四条不变量，全部通过返回 nil，
// 否则返回可用 errors.Is(err, ErrSelfCheck) 判定的错误。
// 只使用新建的临时实例，可并发调用。
func (g *Histogram) SelfCheck() error {
	seqs := []struct {
		width, anchor int
		vals          []int
	}{
		{10, 0, []int{5, 12, 20, 10, -3, -15}}, // 含负值与右边界
		{7, 3, []int{3, 9, 10, 2, -4, 17}},     // 非零锚点
		{1, 0, []int{-3, -2, -1, 0, 1, 2}},     // 桶宽 1
	}
	for _, s := range seqs {
		if err := checkSeq(s.width, s.anchor, s.vals); err != nil {
			return err
		}
	}
	// 不变量 4：width<=0 必须整体失败且无半成品。
	if bad, err := New(0, 0); err == nil || bad != nil {
		return fmt.Errorf("%w: New(0,0) not rejected cleanly", ErrSelfCheck)
	}
	return nil
}

// checkSeq 在临时实例上跑一条序列，核验不变量 1/2/3。
// 序列为稠密序列：范围内每个桶都被命中，可完整核验不变量 3。
func checkSeq(width, anchor int, vals []int) error {
	h, err := New(width, anchor)
	if err != nil {
		return err
	}
	batch := map[int]int{}
	for _, v := range vals {
		h.Insert(v)
		batch[bucket.Number(v, anchor, width)]++
	}
	lo, hi, ok := h.Range()
	if !ok {
		return fmt.Errorf("%w: range empty after inserts", ErrSelfCheck)
	}
	sum := 0
	for k := lo; k <= hi; k++ {
		c := h.BucketCount(k)
		if c != batch[k] { // 不变量 1：与批量重算逐桶一致
			return fmt.Errorf("%w: bucket %d count %d != batch %d", ErrSelfCheck, k, c, batch[k])
		}
		if c <= 0 { // 不变量 3：范围内每个桶都被命中过
			return fmt.Errorf("%w: bucket %d in range never hit", ErrSelfCheck, k)
		}
		sum += c
	}
	if sum != len(vals) || h.Total() != len(vals) { // 不变量 2：计数守恒
		return fmt.Errorf("%w: total %d != inserts %d", ErrSelfCheck, h.Total(), len(vals))
	}
	if h.BucketCount(lo-1) != 0 || h.BucketCount(hi+1) != 0 { // 不变量 3：范围外必为 0
		return fmt.Errorf("%w: nonzero count outside range", ErrSelfCheck)
	}
	return nil
}

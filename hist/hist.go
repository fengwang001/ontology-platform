// Package hist 维护等宽直方图的桶计数与已分配桶号范围 [min, max]。
// 依赖 bucket 包做桶号定位，不反向依赖。
package hist

import (
	"errors"
	"sync"

	"ontology/bucket"
)

// ErrWidth 是 width <= 0 时的可判定哨兵错误。
var ErrWidth = errors.New("hist: width must be > 0")

// Hist 是增量维护的等宽直方图。读方法可并发调用。
type Hist struct {
	mu     sync.RWMutex
	width  int
	anchor int
	count  map[int]int
	total  int
	min    int
	max    int
	empty  bool
	// lastChecked 记录最近一次 Insert 为定位目标桶而检查过的桶个数。
	// 非导出字段，不出现在任何公开接口中；仅同包测试可直接读取。
	lastChecked int
}

// New 创建空直方图；width <= 0 时整体失败，不留半成品状态。
func New(width, anchor int) (*Hist, error) {
	if width <= 0 {
		return nil, ErrWidth
	}
	return &Hist{width: width, anchor: anchor, count: map[int]int{}, empty: true}, nil
}

// Insert 把值 v 计入其桶；k 越出 [min, max] 时扩展范围以包含 k，不丢计数。
func (h *Hist) Insert(v int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	k := bucket.Number(v, h.anchor, h.width)
	h.lastChecked = 1 // map 直接定位：只检查目标桶这 1 个桶，与已分配桶数无关
	h.count[k]++
	h.total++
	switch {
	case h.empty:
		h.min, h.max, h.empty = k, k, false
	case k < h.min:
		h.min = k
	case k > h.max:
		h.max = k
	}
}

// Count 返回桶 k 的计数，未出现的桶为 0。
func (h *Hist) Count(k int) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.count[k]
}

// Range 返回已分配桶号范围 [min, max]；没有任何值时 ok 为 false。
func (h *Hist) Range() (min, max int, ok bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.min, h.max, !h.empty
}

// Total 返回所有桶计数之和，即 Insert 过的值总数。
func (h *Hist) Total() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.total
}

// VerifyLocateBound 自检：对已分配 m 个桶的直方图 Insert 一个落入全新桶的值，
// 报告定位检查过的桶个数是否始终不超过 bound（与 m 无关的小常数）。
// 它只返回判定结论，不暴露 lastChecked 的数值。
func VerifyLocateBound(ms []int, bound int) bool {
	for _, m := range ms {
		h, err := New(1, 0)
		if err != nil {
			return false
		}
		for i := 0; i < m; i++ {
			h.Insert(i * 1_000_000) // 彼此相距很远，铺出 m 个已分配桶
		}
		h.Insert(-1) // 落入全新桶
		if h.lastChecked > bound {
			return false
		}
	}
	return true
}

// Package hist 增量维护等宽直方图的桶计数与已分配桶号范围。
package hist

import (
	"sync"

	"ontology/bucket"
)

// Hist 维护 count 映射与 [min, max] 桶号范围（闭区间）。
type Hist struct {
	mu      sync.RWMutex
	width   int
	anchor  int
	count   map[int]int
	min     int
	max     int
	empty   bool
	total   int
	checked int // 最近一次 Insert 为定位目标桶检查过的桶个数（非导出，仅供包内测试观测）
}

// New 构造空直方图。调用方保证 width > 0。
func New(width, anchor int) *Hist {
	return &Hist{width: width, anchor: anchor, count: map[int]int{}, empty: true}
}

// Insert 把 v 计入其桶；越界时扩展 [min, max]，不丢失任何计数。
func (h *Hist) Insert(v int) {
	k := bucket.Index(h.width, h.anchor, v)
	h.mu.Lock()
	h.checked = 1 // map 直接定位：只检查目标桶一个
	h.count[k]++
	h.total++
	if h.empty {
		h.min, h.max, h.empty = k, k, false
	} else if k < h.min {
		h.min = k
	} else if k > h.max {
		h.max = k
	}
	h.mu.Unlock()
}

// Count 返回桶 k 的计数，未出现的桶为 0。
func (h *Hist) Count(k int) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.count[k]
}

// Range 返回已分配桶号范围 [min, max]；空时 ok=false。
func (h *Hist) Range() (min, max int, ok bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.min, h.max, !h.empty
}

// Total 返回所有桶计数之和。
func (h *Hist) Total() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.total
}

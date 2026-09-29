// Package histogram 提供等宽分桶的增量直方图。
//
// 值域 [0, upper) 按 width 等宽切分，桶左闭右开：[i*width, (i+1)*width)。
// 值恰好等于某桶左边界归该桶；达到或超过 upper 的值进独立溢出桶；
// 负值非法。加值使对应桶计数加一，撤值减一，减到零时桶从直方图消失。
package histogram

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// 可区分的失败原因，配合 errors.Is 判定。
var (
	// ErrNonPositiveWidth 桶宽非正。
	ErrNonPositiveWidth = errors.New("histogram: bucket width must be positive")
	// ErrInvalidUpper 值域上界非正，或不是桶宽的整数倍。
	ErrInvalidUpper = errors.New("histogram: upper bound must be positive and a multiple of width")
	// ErrInvalidMaxActive 活跃桶数上限非正。
	ErrInvalidMaxActive = errors.New("histogram: max active buckets must be positive")
	// ErrNegativeValue 值为负。
	ErrNegativeValue = errors.New("histogram: negative value")
	// ErrBucketNotFound 撤回不存在的桶。
	ErrBucketNotFound = errors.New("histogram: bucket not found")
	// ErrTooManyBuckets 活跃桶数超限。
	ErrTooManyBuckets = errors.New("histogram: too many active buckets")
)

// Bucket 是直方图中一个活跃桶的不可变快照。
type Bucket struct {
	Index    int64 // 常规桶为 0..numBuckets-1；溢出桶为 numBuckets
	Lower    int64 // 左边界（含）；溢出桶为值域上界
	Upper    int64 // 右边界（不含）；溢出桶为 -1 表示无上界
	Count    uint64
	Overflow bool
}

// overflowSentinel 是溢出桶在内部计数表中的键。
const overflowSentinel = -1

// Histogram 是并发安全的等宽增量直方图。
type Histogram struct {
	mu         sync.RWMutex
	width      int64
	upper      int64
	numBuckets int64
	maxActive  int
	counts     map[int64]uint64 // 常规桶下标或 overflowSentinel -> 计数
}

// New 创建直方图。upper 必须为正且是 width 的整数倍；
// maxActive 为活跃桶（含溢出桶）数量上限。参数非法时整体拒绝。
func New(width, upper int64, maxActive int) (*Histogram, error) {
	if width <= 0 {
		return nil, fmt.Errorf("%w: got %d", ErrNonPositiveWidth, width)
	}
	if upper <= 0 || upper%width != 0 {
		return nil, fmt.Errorf("%w: got upper=%d width=%d", ErrInvalidUpper, upper, width)
	}
	if maxActive <= 0 {
		return nil, fmt.Errorf("%w: got %d", ErrInvalidMaxActive, maxActive)
	}
	return &Histogram{
		width:      width,
		upper:      upper,
		numBuckets: upper / width,
		maxActive:  maxActive,
		counts:     make(map[int64]uint64),
	}, nil
}

// locate 返回值 v 归属的桶下标；负值返回错误。
// 左闭右开：v 等于桶左边界归该桶；v >= upper 归溢出桶。
func (h *Histogram) locate(v int64) (int64, error) {
	if v < 0 {
		return 0, fmt.Errorf("%w: got %d", ErrNegativeValue, v)
	}
	if v >= h.upper {
		return overflowSentinel, nil
	}
	return v / h.width, nil
}

// Add 将值 v 计入对应桶。非法值或活跃桶数超限时整体拒绝，直方图不变。
func (h *Histogram) Add(v int64) error {
	idx, err := h.locate(v)
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.counts[idx]; !ok && len(h.counts) >= h.maxActive {
		return fmt.Errorf("%w: limit %d", ErrTooManyBuckets, h.maxActive)
	}
	h.counts[idx]++
	return nil
}

// Remove 撤回值 v。桶不存在或值为负时整体拒绝，直方图不变；
// 计数减到零时桶从直方图消失。
func (h *Histogram) Remove(v int64) error {
	idx, err := h.locate(v)
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	c, ok := h.counts[idx]
	if !ok {
		return fmt.Errorf("%w: value %d", ErrBucketNotFound, v)
	}
	if c == 1 {
		delete(h.counts, idx)
	} else {
		h.counts[idx] = c - 1
	}
	return nil
}

// BucketOf 返回值 v 归属桶的快照；桶不存在时 ok=false（而非返回零计数）。
func (h *Histogram) BucketOf(v int64) (Bucket, bool, error) {
	idx, err := h.locate(v)
	if err != nil {
		return Bucket{}, false, err
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	c, ok := h.counts[idx]
	if !ok {
		return Bucket{}, false, nil
	}
	return h.describe(idx, c), true, nil
}

// Buckets 返回全部活跃桶的一致性快照，按下标升序，溢出桶在最后。
func (h *Histogram) Buckets() []Bucket {
	h.mu.RLock()
	defer h.mu.RUnlock()
	idxs := make([]int64, 0, len(h.counts))
	for idx := range h.counts {
		idxs = append(idxs, idx)
	}
	sort.Slice(idxs, func(i, j int) bool {
		a, b := idxs[i], idxs[j]
		if a == overflowSentinel {
			return false
		}
		if b == overflowSentinel {
			return true
		}
		return a < b
	})
	out := make([]Bucket, 0, len(idxs))
	for _, idx := range idxs {
		out = append(out, h.describe(idx, h.counts[idx]))
	}
	return out
}

// Len 返回当前活跃桶数量（含溢出桶）。
func (h *Histogram) Len() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.counts)
}

// describe 构造桶快照，调用方须持有读锁或写锁。
func (h *Histogram) describe(idx int64, count uint64) Bucket {
	if idx == overflowSentinel {
		return Bucket{Index: h.numBuckets, Lower: h.upper, Upper: -1, Count: count, Overflow: true}
	}
	return Bucket{
		Index: idx,
		Lower: idx * h.width,
		Upper: (idx + 1) * h.width,
		Count: count,
	}
}

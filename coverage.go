// Package ontology 实现区间覆盖计数器。
//
// 维护一堆可以互相重叠的一维区间（int64 端点，左闭右开 [lo, hi)），
// 核心问题是回答“某个点被多少个区间同时覆盖”。区间绝不合并、绝不
// 去重：同一个区间被添加三次就是三层覆盖。
//
// 端点语义（咬死）：区间 [lo, hi) 让 lo 处的覆盖数加一、hi 处减一，
// 因此 CountAt(lo) 包含该区间、CountAt(hi) 不包含。多个区间在同一点
// 同时开始与结束时，规定“先生效开始、再生效结束”：该点计入在此处
// 开始的区间，不计入在此处结束的区间。由于内部只维护每个端点的净
// 增减量（加法可交换），CountAt 与 Segments 的结果与添加顺序无关。
//
// 重数：同一区间添加 n 次，其覆盖范围内每一点的计数恰好增加 n；
// Remove 一次只减一层，减到零才真正消失；多减返回 ErrNotPresent。
//
// 溢出规避：实现从不计算区间长度、中点或宽度，端点允许取到
// math.MinInt64 与 math.MaxInt64。“覆盖总量”（各区间长度之和）可能
// 远超 int64，本实现完全不物化该值——覆盖信息只以端点净增减量与前缀
// 和的形式存在，前缀和的上界是区间总层数而非总长度，因此不会溢出。
package ontology

import (
	"errors"
	"fmt"
	"slices"
	"sync"
)

// ErrEmptyInterval 表示区间为空或反向（hi <= lo），可判定错误。
var ErrEmptyInterval = errors.New("ontology: empty interval (hi <= lo)")

// ErrNotPresent 表示要移除的区间当前不存在（重数已为零），可判定错误。
var ErrNotPresent = errors.New("ontology: interval not present")

// Interval 是一个左闭右开区间 [Lo, Hi)。
type Interval struct {
	Lo int64
	Hi int64
}

// Segment 是覆盖数分段常量视图中的一段：[Lo, Hi) 上覆盖数恒为 Count。
type Segment struct {
	Lo    int64
	Hi    int64
	Count int64
}

// Counter 是并发安全的区间覆盖计数器，状态全部在进程内存中。
//
// 内部表示：
//   - mult：每个精确区间的重数（用于 Remove 的可判定错误）；
//   - delta：每个端点的净增减量（lo 处 +1、hi 处 -1，按重数加权）；
//   - ends/pref：物化后的有序端点表与前缀和，供 CountAt 二分；
//     仅在变更后首次查询时重建（懒物化），查询本身不扫描全部区间。
//
// MaxCoverage 的维护方式：最大值与达到最大值的首个位置在物化时一并
// 算出并缓存；未发生变更时 MaxCoverage 直接读缓存，不会每次全表重算。
type Counter struct {
	mu    sync.RWMutex
	mult  map[Interval]int64
	delta map[int64]int64
	dirty bool

	ends []int64 // 有序端点表（净增减量非零的端点）
	pref []int64 // 与 ends 对齐的前缀和，即 [ends[i], ends[i+1]) 上的覆盖数

	maxCount int64 // 当前最大覆盖层数（缓存）
	maxStart int64 // 达到最大覆盖层数的首个位置（缓存）
	hasMax   bool
}

// New 返回一个空的区间覆盖计数器。
func New() *Counter {
	return &Counter{
		mult:  make(map[Interval]int64),
		delta: make(map[int64]int64),
	}
}

// Add 把区间 [lo, hi) 加入一层覆盖。hi <= lo 时返回 ErrEmptyInterval。
func (c *Counter) Add(lo, hi int64) error {
	if hi <= lo {
		return fmt.Errorf("%w: [%d, %d)", ErrEmptyInterval, lo, hi)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mult[Interval{lo, hi}]++
	c.delta[lo]++
	c.delta[hi]--
	c.dirty = true
	return nil
}

// Remove 把区间 [lo, hi) 减去一层覆盖。区间不存在（重数为零）时返回
// ErrNotPresent，计数不会变成负数。hi <= lo 时返回 ErrEmptyInterval。
func (c *Counter) Remove(lo, hi int64) error {
	if hi <= lo {
		return fmt.Errorf("%w: [%d, %d)", ErrEmptyInterval, lo, hi)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	iv := Interval{lo, hi}
	n := c.mult[iv]
	if n == 0 {
		return fmt.Errorf("%w: [%d, %d)", ErrNotPresent, lo, hi)
	}
	if n == 1 {
		delete(c.mult, iv)
	} else {
		c.mult[iv] = n - 1
	}
	c.delta[lo]--
	c.delta[hi]++
	c.dirty = true
	return nil
}

// ensureMaterialized 在脏标记置位时重建有序端点表与前缀和缓存，
// 同时维护 MaxCoverage 的缓存值。调用方不持有锁。
func (c *Counter) ensureMaterialized() {
	c.mu.RLock()
	dirty := c.dirty
	c.mu.RUnlock()
	if !dirty {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.dirty {
		return
	}
	c.materializeLocked()
	c.dirty = false
}

// materializeLocked 重建 ends/pref 与 MaxCoverage 缓存。调用方须持有写锁。
func (c *Counter) materializeLocked() {
	ends := make([]int64, 0, len(c.delta))
	for p, d := range c.delta {
		if d != 0 {
			ends = append(ends, p)
		}
	}
	slices.Sort(ends)
	pref := make([]int64, len(ends))
	var sum int64
	var maxCount, maxStart int64
	var hasMax bool
	for i, p := range ends {
		sum += c.delta[p]
		pref[i] = sum
		if !hasMax || sum > maxCount {
			maxCount, maxStart, hasMax = sum, p, true
		}
	}
	c.ends = ends
	c.pref = pref
	c.maxCount = maxCount
	c.maxStart = maxStart
	c.hasMax = hasMax
}

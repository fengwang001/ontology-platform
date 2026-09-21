package coverage

import (
	"sort"
	"sync"
)

// interval 标识一层半开区间 [Lo, Hi)。
type interval struct {
	Lo int64
	Hi int64
}

// Counter 是并发安全的区间覆盖计数器，零值即可使用。
//
// 内部表示（在锁内始终保持自洽）：
//   - ends 为严格升序、无重复的全部端点（含开始与结束）；
//   - delta[e] 为端点 e 处的覆盖增量：开始为 +1，结束为 -1；
//   - cum[i] 为覆盖数在 ends[i] 处跃变后的值，即区间
//     [ends[i], ends[i+1]) 内的覆盖层数；
//   - refs 记录每个相同区间当前被叠加的层数；
//   - max 维护当前最大覆盖层数及其一个起始端点。
type Counter struct {
	mu    sync.RWMutex
	ends  []int64
	delta map[int64]int64
	cum   []int64
	refs  map[interval]int64
	max   int64
	maxAt int64
	dirty bool
}

// New 返回空计数器。
func New() *Counter {
	return &Counter{
		delta: make(map[int64]int64),
		refs:  make(map[interval]int64),
	}
}

func (c *Counter) initLocked() {
	if c.delta == nil {
		c.delta = make(map[int64]int64)
	}
	if c.refs == nil {
		c.refs = make(map[interval]int64)
	}
}

// Add 叠加一层区间 [lo, hi)。同一区间可重复添加，覆盖数逐层累加。
func (c *Counter) Add(lo, hi int64) error {
	if hi <= lo {
		return ErrEmptyInterval
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.initLocked()

	key := interval{Lo: lo, Hi: hi}
	c.refs[key]++

	c.applyDelta(lo, +1)
	c.applyDelta(hi, -1)

	// 覆盖数只在 [lo, hi) 内变化；最大值缓存的维护见 MaxCoverage：
	// 连续多次写入只置脏，读取时合并重算一次。
	c.dirty = true
	return nil
}

// Remove 移除一层此前添加的区间。多减（层数将为负）返回
// ErrOverRemoved；区间未被跟踪返回 ErrNotTracked。两种错误都不会
// 改变计数，任何点的覆盖数不会变成负数。
func (c *Counter) Remove(lo, hi int64) error {
	if hi <= lo {
		return ErrEmptyInterval
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.initLocked()

	key := interval{Lo: lo, Hi: hi}
	n, ok := c.refs[key]
	if !ok {
		return ErrNotTracked
	}
	if n <= 0 {
		return ErrOverRemoved
	}

	c.refs[key] = n - 1
	c.applyDelta(lo, -1)
	c.applyDelta(hi, +1)

	// 只会在 [lo, hi) 内降低覆盖数；若恰好削去最大层，则标记脏，
	// 下次 MaxCoverage 查询时做一次 O(端点数) 重算，而非每次全表重算。
	if n-1 == 0 {
		delete(c.refs, key)
	}
	c.dirty = true
	return nil
}

// applyDelta 把端点 p 处的增量增加 d，并维护严格升序的 ends。
// delta 变为 0 的端点立即删除，保证端点表无冗余、前缀序列唯一。
func (c *Counter) applyDelta(p, d int64) {
	idx := sort.Search(len(c.ends), func(i int) bool { return c.ends[i] >= p })
	v := c.delta[p] + d
	if v == 0 {
		delete(c.delta, p)
		if idx < len(c.ends) && c.ends[idx] == p {
			old := c.deltaAt(c.cum, idx)
			c.ends = append(c.ends[:idx], c.ends[idx+1:]...)
			c.cum = append(c.cum[:idx], c.cum[idx+1:]...)
			for j := idx; j < len(c.cum); j++ {
				c.cum[j] -= old
			}
		}
		return
	}
	c.delta[p] = v
	if idx == len(c.ends) || c.ends[idx] != p {
		c.ends = append(c.ends, 0)
		copy(c.ends[idx+1:], c.ends[idx:])
		c.ends[idx] = p
		before := int64(0)
		if idx > 0 {
			before = c.cum[idx-1]
		}
		c.cum = append(c.cum, 0)
		copy(c.cum[idx+1:], c.cum[idx:])
		c.cum[idx] = before + v
		for j := idx + 1; j < len(c.cum); j++ {
			c.cum[j] += d
		}
		return
	}
	for j := idx; j < len(c.cum); j++ {
		c.cum[j] += d
	}
}

// deltaAt 由前缀序列反推下标 idx 处的原始增量。
func (c *Counter) deltaAt(cum []int64, idx int) int64 {
	if idx == 0 {
		return cum[0]
	}
	return cum[idx] - cum[idx-1]
}

// indexLocked 返回端点 p 在 ends 中的下标，要求 p 必须存在。
func (c *Counter) indexLocked(p int64) int {
	idx := sort.Search(len(c.ends), func(i int) bool { return c.ends[i] >= p })
	if idx < len(c.ends) && c.ends[idx] == p {
		return idx
	}
	return -1
}

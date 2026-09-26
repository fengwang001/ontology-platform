// Package res 预留核心：容量、已激活预留集合、区间峰值计算。不依赖其他包。
package res

import "sort"

// Interval 是一条已激活预留：[Start, End) 左闭右开，占用 Need 单位资源。
type Interval struct {
	Start, End, Need int64
}

// Core 维护容量与已激活预留集合。
// ivs 按 Start 升序；maxEnd[i] = max(ivs[0..i].End)，用于按端点有序定位重叠候选，
// 避免整表扫描。checked 记录最近一次 CanAdd 扫描阶段检查（比较）的已激活预留个数，
// 非导出，不出现在任何公开接口。
type Core struct {
	cap     int64
	ivs     []Interval
	maxEnd  []int64
	checked int
}

func NewCore(capacity int64) *Core { return &Core{cap: capacity} }

func (c *Core) Capacity() int64 { return c.cap }

func (c *Core) Count() int { return len(c.ivs) }

// 返回首个 Start >= x 的下标。
func (c *Core) lowerStart(x int64) int {
	return sort.Search(len(c.ivs), func(i int) bool { return c.ivs[i].Start >= x })
}

// 返回首个 maxEnd > x 的下标。
func (c *Core) lowerMaxEnd(x int64) int {
	return sort.Search(len(c.maxEnd), func(i int) bool { return c.maxEnd[i] > x })
}

// Peak 计算 [s, e) 内 load 的峰值（覆盖某点的所有预留 Need 之和的最大值）。
// 只检查可能重叠的候选：Start < e 且属于 maxEnd > s 的后缀。
func (c *Core) Peak(s, e int64) int64 {
	lo, hi := c.lowerMaxEnd(s), c.lowerStart(e)
	type ev struct {
		x, d int64
	}
	var evs []ev
	c.checked = 0
	for i := lo; i < hi; i++ {
		c.checked++ // 扫描阶段每比较一条已激活预留计一次
		iv := c.ivs[i]
		if iv.End <= s || iv.Start >= e {
			continue
		}
		a, b := iv.Start, iv.End
		if a < s {
			a = s
		}
		if b > e {
			b = e
		}
		evs = append(evs, ev{a, iv.Need}, ev{b, -iv.Need})
	}
	sort.Slice(evs, func(i, j int) bool { return evs[i].x < evs[j].x })
	var load, peak int64
	for i := 0; i < len(evs); {
		j := i
		for j < len(evs) && evs[j].x == evs[i].x { // 同点的结束与开始先合并再施加
			load += evs[j].d
			j++
		}
		if load > peak {
			peak = load
		}
		i = j
	}
	return peak
}

// CanAdd 报告在 [s, e) 再加入 need 是否不越容量。
func (c *Core) CanAdd(s, e, need int64) bool { return c.Peak(s, e)+need <= c.cap }

// Add 插入一条预留，保持 ivs 按 Start 有序并维护 maxEnd。
func (c *Core) Add(s, e, need int64) {
	i := c.lowerStart(s)
	c.ivs = append(c.ivs, Interval{})
	copy(c.ivs[i+1:], c.ivs[i:])
	c.ivs[i] = Interval{s, e, need}
	c.rebuild(i)
}

// Remove 删除一条与 (s, e, need) 完全相同的预留，返回是否找到。
func (c *Core) Remove(s, e, need int64) bool {
	for i := c.lowerStart(s); i < len(c.ivs) && c.ivs[i].Start == s; i++ {
		if c.ivs[i].End == e && c.ivs[i].Need == need {
			copy(c.ivs[i:], c.ivs[i+1:])
			c.ivs = c.ivs[:len(c.ivs)-1]
			c.rebuild(i)
			return true
		}
	}
	return false
}

// 从下标 i 起重建 maxEnd 前缀最大值；若刚重新分配（旧值丢失）则从头重建。
func (c *Core) rebuild(i int) {
	if len(c.maxEnd) != len(c.ivs) {
		c.maxEnd = make([]int64, len(c.ivs))
		i = 0
	}
	for ; i < len(c.ivs); i++ {
		m := c.ivs[i].End
		if i > 0 && c.maxEnd[i-1] > m {
			m = c.maxEnd[i-1]
		}
		c.maxEnd[i] = m
	}
}

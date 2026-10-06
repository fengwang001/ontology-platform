package transformer

import "sort"

// slotMap 按整数时刻槽记录功率占用。校验与更新的开销只取决于区间长度，
// 与不相交的预约数量严格无关。
type slotMap map[int]int

func (m slotMap) at(t int) int { return m[t] }

func (m slotMap) add(start, end, delta int) {
	for t := start; t < end; t++ {
		v := m[t] + delta
		if v == 0 {
			delete(m, t)
		} else {
			m[t] = v
		}
	}
}

// capacityTable 变压器容量表：按生效时刻升序，t 时刻容量取生效时刻不晚于 t 的最新一条。
type capacityTable struct {
	times []int
	caps  []int
}

// at 二分查找容量，compares 非空时累计比较次数（供性能验证）。
func (c *capacityTable) at(t int, compares *int) int {
	lo, hi := 0, len(c.times)
	idx := -1
	for lo < hi {
		if compares != nil {
			*compares++
		}
		mid := lo + (hi-lo)/2
		if c.times[mid] <= t {
			idx = mid
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if idx < 0 {
		return 0
	}
	return c.caps[idx]
}

// set 登记一条容量记录；生效时刻相同则替换。
func (c *capacityTable) set(t, value int) {
	i := sort.SearchInts(c.times, t)
	if i < len(c.times) && c.times[i] == t {
		c.caps[i] = value
		return
	}
	c.times = append(c.times, 0)
	c.caps = append(c.caps, 0)
	copy(c.times[i+1:], c.times[i:])
	copy(c.caps[i+1:], c.caps[i:])
	c.times[i] = t
	c.caps[i] = value
}

// nextAfter 返回生效时刻严格大于 t 的第一条记录时刻。
func (c *capacityTable) nextAfter(t int) (int, bool) {
	i := sort.Search(len(c.times), func(i int) bool { return c.times[i] > t })
	if i < len(c.times) {
		return c.times[i], true
	}
	return 0, false
}

package coverage

// MaxResult 描述最大覆盖层数及其一个起始位置。
type MaxResult struct {
	// Count 为当前最大覆盖层数；无区间时为 0。
	Count int64
	// Start 为达到该层数的某一段左端点（半开，含此点）。
	Start int64
	// Found 表示是否存在非零覆盖。
	Found bool
}

// MaxCoverage 返回当前最大覆盖层数及达到该层数的一个起始位置。
//
// 维护方式：Add 只可能抬高覆盖数，因此增量更新最大值（O(1)）；
// Remove 可能削平最大值，此时只置脏标记，下次读取时做一次
// O(端点数) 的扫描重算并缓存，而不是每次调用都全表重算。
func (c *Counter) MaxCoverage() MaxResult {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.dirty {
		c.recomputeMaxLocked()
	}
	if c.max <= 0 {
		return MaxResult{}
	}
	return MaxResult{Count: c.max, Start: c.maxAt, Found: true}
}

func (c *Counter) recomputeMaxLocked() {
	var best int64
	bestAt := int64(0)
	for i, v := range c.cum {
		if v > best {
			best = v
			bestAt = c.ends[i]
		}
	}
	c.max = best
	c.maxAt = bestAt
	c.dirty = false
}

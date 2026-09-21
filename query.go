package ontology

// CountAt 返回点 p 当前被多少个区间同时覆盖，以及本次查询在有序端点
// 表上实际检查的端点个数（checked）。查询在物化后的有序端点表上二分，
// 不线性扫描全部区间或全部段，checked 为端点数的对数量级。
//
// 端点语义：区间 [lo, hi) 计入 CountAt(lo)，不计入 CountAt(hi)。
func (c *Counter) CountAt(p int64) (count int64, checked int) {
	c.ensureMaterialized()
	c.mu.RLock()
	defer c.mu.RUnlock()
	// 在有序端点表上二分：找最后一个满足 ends[i] <= p 的下标。
	lo, hi := 0, len(c.ends)
	for lo < hi {
		checked++
		mid := int(uint(lo+hi) >> 1)
		if c.ends[mid] <= p {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == 0 {
		return 0, checked
	}
	return c.pref[lo-1], checked
}

// Segments 返回覆盖数分段常量的规范视图：一组 (Lo, Hi, Count) 三元组，
// 按 Lo 升序、两两不重叠、覆盖全部非零区域；相邻且 Count 相同的段已
// 合并为一段，Count 为零的段不出现。任意添加顺序下结果逐元素一致。
//
// 返回的切片是新分配的，与内部状态互不影响。
func (c *Counter) Segments() []Segment {
	c.ensureMaterialized()
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.segmentsLocked()
}

// MaxCoverage 返回当前最大覆盖层数及达到该层数的一个起始位置。
// 第三个返回值表示当前是否存在任何覆盖（计数器为空时为 false）。
// 结果来自物化时维护的缓存，未发生变更时不会全表重算。
func (c *Counter) MaxCoverage() (count int64, start int64, ok bool) {
	c.ensureMaterialized()
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.maxCount, c.maxStart, c.hasMax
}

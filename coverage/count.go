package coverage

// CountResult 是一次点查询的结果。
type CountResult struct {
	// Count 为 point 处的覆盖层数。
	Count int64
	// EndpointsExamined 为本次二分查询检查过的端点个数，
	// 数量级为 O(log(端点数)+1)，绝不线性扫描全部区间或全部段。
	EndpointsExamined int
}

// CountAt 返回 point 处的覆盖重数。
//
// 半开语义：区间 [lo, hi) 在 lo 处计入、在 hi 处不计入。同一点既有
// 开始又有结束时按“先加后减”取值，结果与添加顺序无关。
func (c *Counter) CountAt(point int64) CountResult {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// 在严格升序的 ends 上做二分：求满足 ends[i] <= point 的最大 i，
	// 覆盖数即 cum[i]；point 早于第一个端点时为 0。
	lo, hi := 0, len(c.ends)
	probes := 0
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		probes++
		if c.ends[mid] <= point {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == 0 {
		return CountResult{EndpointsExamined: probes}
	}
	return CountResult{Count: c.cum[lo-1], EndpointsExamined: probes}
}

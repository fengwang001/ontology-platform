package coverage

// Segment 表示覆盖数为常量的一段半开区间 [Lo, Hi)。
type Segment struct {
	Lo    int64
	Hi    int64
	Count int64
}

// Segments 返回覆盖数分段常量的规范视图：
//   - 按 Lo 升序，两两不重叠；
//   - 覆盖全部非零区域，覆盖数为 0 的段不出现；
//   - 相邻且 Count 相同的段合并为一段。
//
// 因为内部端点表与前缀序列与添加顺序无关，任意添加顺序下
// Segments 的结果逐元素一致。返回切片为独立副本。
func (c *Counter) Segments() []Segment {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.SegmentsRlocked()
}

// SegmentsRlocked 与 Segments 相同，但调用方必须已持有读锁。
func (c *Counter) SegmentsRlocked() []Segment {
	if len(c.ends) == 0 {
		return []Segment{}
	}

	// 在累计段上做一次游标扫描：跳过 0 段，合并相邻同值段。
	// 段 [ends[i], ends[i+1]) 的覆盖数为 cum[i]；cum 末项恒为 0。
	out := make([]Segment, 0)
	i := 0
	for i < len(c.ends)-1 {
		if c.cum[i] <= 0 {
			i++
			continue
		}
		start := c.ends[i]
		count := c.cum[i]
		j := i
		for j < len(c.ends)-1 && c.cum[j] == count {
			j++
		}
		out = append(out, Segment{Lo: start, Hi: c.ends[j], Count: count})
		i = j
	}
	return out
}

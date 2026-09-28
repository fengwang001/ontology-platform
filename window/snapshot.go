package window

import "sort"

// buildState 从计数器内部状态构造完整快照（调用方必须持有读锁或独占副本）。
//
// 结果切片按 (Key, Start) 字典序排序、输出历史按 Seq 排序（提交顺序保证），
// 且所有切片均为独立拷贝，因此多个并发读取者拿到的内容逐字段一致，
// 快照之后的任何处理都不会影响已返回的 State。
func buildState(c *Counter) State {
	st := State{
		WatermarkSet: c.wmSet,
		Watermark:    c.wm,
		Dropped:      c.dropped,
		Results:      make([]WindowResult, 0, c.resultCount()),
		Emissions:    make([]Emission, len(c.emissions)),
	}
	copy(st.Emissions, c.emissions)
	for _, m := range c.results {
		for _, r := range m {
			st.Results = append(st.Results, r)
		}
	}
	sort.Slice(st.Results, func(i, j int) bool {
		if st.Results[i].Key != st.Results[j].Key {
			return st.Results[i].Key < st.Results[j].Key
		}
		return st.Results[i].Start < st.Results[j].Start
	})
	return st
}

// resultCount 返回已触发且尚未清除的窗口结果总数。
func (c *Counter) resultCount() int {
	n := 0
	for _, m := range c.results {
		n += len(m)
	}
	return n
}

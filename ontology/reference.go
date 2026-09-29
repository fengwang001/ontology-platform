package ontology

// SplitSorted 是参考实现：给定已按 Ts 升序去重的时间戳序列，
// 按"相邻事件时间差 <= gap 即相连"切分会话。
//
// 相连关系是“时间差绝对值 <= gap”。对升序序列只需比较相邻元素：
// 任意非相邻两点若时间差 <= gap，则中间所有相邻点差必然 <= gap，
// 因此会话恰好等于按相邻差 > gap 断开的连续段（连通分量）。
// gap <= 0 返回 ErrNonPositiveGap（参考实现不设 Key 字段，由调用方填充）。
func SplitSorted(sortedTs []int64, gap int64) ([]Session, error) {
	if gap <= 0 {
		return nil, ErrNonPositiveGap
	}
	if len(sortedTs) == 0 {
		return []Session{}, nil
	}

	sessions := make([]Session, 0)
	start := 0
	flush := func(end int) {
		events := make([]int64, end-start+1)
		copy(events, sortedTs[start:end+1])
		sessions = append(sessions, Session{
			Start:  sortedTs[start],
			End:    sortedTs[end],
			Events: events,
		})
	}

	for i := 1; i < len(sortedTs); i++ {
		if sortedTs[i]-sortedTs[i-1] > gap {
			flush(i - 1)
			start = i
		}
	}
	flush(len(sortedTs) - 1)
	return sessions, nil
}

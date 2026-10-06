package baggage

// SplitSections 按直挂规则把行程拆成若干托运段，返回航段下标区间 [start, end)。
// 纯函数，开销 O(航段数)。
func SplitSections(segs []Segment, airports map[string]Airport, cfg Config) [][2]int {
	var out [][2]int
	start := 0
	for i := 1; i < len(segs); i++ {
		station := segs[i].From
		conn := segs[i].Depart - segs[i-1].Arrive
		through := !airports[station].Customs &&
			segs[i-1].PNR == segs[i].PNR &&
			conn >= cfg.MinConn && conn <= cfg.MaxConn
		if !through {
			out = append(out, [2]int{start, i})
			start = i
		}
	}
	return append(out, [2]int{start, len(segs)})
}

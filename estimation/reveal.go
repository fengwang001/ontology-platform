package estimation

// evaluate 依据计票簿的增量计数计算揭示结果。只扫描牌组（至多
// 20 张），与参与者总数无关。
//
// 判定次序：无数值牌 -> 无有效票/终局无结果；数值牌全同 -> 共识；
// 最大最小在牌组中相邻 -> 收敛（取较大者）；轮次用尽 -> 强制取值
// （按牌组位置排序的下中位）；其余 -> 分歧。
func (d Deck) evaluate(box *ballotBox, round, maxRounds int, at int64, trig RevealTrigger) *RevealOutcome {
	out := &RevealOutcome{
		Round:        round,
		RevealedAt:   at,
		Trigger:      trig,
		Distribution: box.distribution(),
		NumericVotes: box.numeric,
	}
	switch {
	case box.numeric == 0:
		if round >= maxRounds {
			out.Kind = ResultFinalNoResult
		} else {
			out.Kind = ResultNoValidVotes
		}
	case box.distinct == 1:
		out.Kind = ResultConsensus
		for _, v := range d.values {
			if box.dist[Card(v)] > 0 {
				out.Value = v
				break
			}
		}
		out.HasValue = true
	default:
		lo, hi := 0, len(d.values)-1
		for box.dist[Card(d.values[lo])] == 0 {
			lo++
		}
		for box.dist[Card(d.values[hi])] == 0 {
			hi--
		}
		switch {
		case hi-lo == 1:
			out.Kind = ResultConverged
			out.Value = d.values[hi]
			out.HasValue = true
		case round >= maxRounds:
			out.Kind = ResultForced
			out.Value = d.lowerMedian(box)
			out.HasValue = true
		default:
			out.Kind = ResultDiverged
		}
	}
	out.IssueEnded = out.Kind.EndsIssue()
	return out
}

// lowerMedian 返回全部数值牌票按牌组位置排序后的下中位
// （偶数张时取靠前的那一张）。牌组严格递增，故按位置升序
// 累计计数，首个累计数达到 (n+1)/2 的牌即为所求。
func (d Deck) lowerMedian(box *ballotBox) int {
	target := (box.numeric + 1) / 2
	cum := 0
	for _, v := range d.values {
		cum += box.dist[Card(v)]
		if cum >= target {
			return v
		}
	}
	return 0 // 不可达：调用前已保证 numeric >= 2
}

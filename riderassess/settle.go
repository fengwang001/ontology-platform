package riderassess

// 本文件负责周期结算与定级、以及回溯补偿的纯逻辑，供 System 与朴素对照模型复用。

// settleBase 返回骑手第 p 个周期结算时使用的"上一周期权益等级"基准。
// 通常取 p-1 周期的权益；回溯可能为某个尚未开始的周期写入覆盖基准，
// 使该周期的下降上限相对一个"本应得到"的更优权益重新计算。
func settleBase(rs *riderState, p int) int {
	if b, ok := rs.baseOverride[p]; ok {
		return b
	}
	if p == 0 {
		return 0 // 首个周期之前骑手权益为最优等级 0
	}
	if b, ok := rs.benefitSnap[p-1]; ok {
		return b
	}
	return 0 // p-1 尚未冻结时不会冻结 p，防御性返回
}

func clampDown(base, grade, maxDown int) int {
	benefit := base
	if grade < base {
		benefit = grade // 等级变好（数字变小）：上升不受限，立即跟进
	} else if grade > base+maxDown {
		benefit = base + maxDown // 等级变差：每周期最多下降 maxDown 级
	}
	return benefit
}

func (s *System) applyFreeze(rs *riderState, p int) {
	score := rs.totals[p]
	grade := s.cfg.gradeOf(score)
	benefit := clampDown(settleBase(rs, p), grade, s.cfg.MaxDownPerCycle)
	rs.scoreSnap[p] = score
	rs.gradeSnap[p] = grade
	rs.benefitSnap[p] = benefit
}

// freezeThrough 冻结骑手所有右端点不晚于 now 的周期。
// 结算只依赖时刻：任何不早于右端点的已接受操作都会把该周期确定下来。
func (s *System) freezeThrough(rs *riderState, now int64) {
	for s.cfg.periodEnd(rs.nextFreeze) <= now {
		p := rs.nextFreeze
		s.applyFreeze(rs, p)
		rs.nextFreeze = p + 1
	}
}

// liveBenefit 在未冻结情形下按时间顺序递推周期 p 本应生效的权益等级。
// 仅按周期数线性递推、无事件扫描，开销不随任何周期的事件数增长。
func (s *System) liveBenefit(rs *riderState, p int) int {
	if b, ok := rs.benefitSnap[p]; ok {
		return b
	}
	// 起点：0、某个覆盖基准周期、或最后一个已冻结周期的下一个，取最大者。
	start := 0
	for k := range rs.baseOverride {
		if k > start && k <= p {
			start = k
		}
	}
	if rs.nextFreeze > start {
		start = rs.nextFreeze
	}
	var base int
	if ov, ok := rs.baseOverride[start]; ok {
		base = ov
	} else {
		base = settleBase(rs, start)
	}
	for q := start; q <= p; q++ {
		base = clampDown(base, s.cfg.gradeOf(rs.totals[q]), s.cfg.MaxDownPerCycle)
	}
	return base
}

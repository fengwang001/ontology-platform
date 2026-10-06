package medschedule

// 计划点数学：固定间隔序列、固定时点日序列、补给重排分段。
// 该文件只做纯函数计算，不持有任何可变状态，因此与朴素模型可独立对照。

const dayLen int64 = 86400

// dayStart 返回不晚于 t 的日起点（86400 的整数倍）；支持非负时间。
func dayStart(t int64) int64 { return t - modNonNeg(t, 86400) }

// modNonNeg 返回非负余数（t >= 0, d > 0）。
func modNonNeg(t, d int64) int64 {
	if t >= 0 {
		return t % d
	}
	return ((t % d) + d) % d
}

// intervalSeg 是固定间隔医嘱的一段生成序列：start + k*H（k>=0）。
// 未截断的段 cut == -1；截断段的全部计划点在 cut 处终止（见 isPointLive）。
type intervalSeg struct {
	start int64
	cut   int64
	// madeUpAt 是触发本段截断的补给点的计划时刻；
	// 段中 t > madeUpAt 的点作废，t <= madeUpAt 的点保留其自然状态（已漏给等）。
	madeUpAt int64
}

// intervalPointAt 返回段内第 k 个计划点时刻。
func intervalPointAt(start, h int64, k int64) int64 { return start + k*h }

// firstIndexAtOrAfter 满足 start+k*h >= lb 的最小 k（k>=0）。
func firstIndexAtOrAfter(start, h, lb int64) int64 {
	if lb <= start {
		return 0
	}
	d := lb - start
	k := d / h
	if d%h != 0 {
		k++
	}
	return k
}

// intervalCandidates 按时间升序生成一段 [lo,hi] 内的计划点。
// start+k*h（k>=0）。仅生成，不判状态。
func intervalCandidates(start, h, lo, hi int64, emit func(t int64)) {
	if h <= 0 || hi < lo || hi < start {
		return
	}
	k := firstIndexAtOrAfter(start, h, lo)
	for t := start + k*h; t <= hi; t += h {
		emit(t)
	}
}

// dailyCandidates 生成固定时点医嘱在 [lo,hi] 内的全部计划点（升序）。
// timesOfDay 为升序去重后的日内秒数；仅生成不早于 createdAt 的点（恰等于保留）。
func dailyCandidates(timesOfDay []int64, createdAt, lo, hi int64, emit func(t int64)) {
	if hi < lo || len(timesOfDay) == 0 {
		return
	}
	d0 := dayStart(max64(lo, createdAt)) - dayLen
	d1 := dayStart(hi) + dayLen
	for day := d0; day <= d1; day += dayLen {
		for _, tod := range timesOfDay {
			t := day + tod
			if t < createdAt || t < lo || t > hi {
				continue
			}
			emit(t)
		}
	}
}

// dailyPointAtOrBefore 返回 <= t 且不早于 createdAt 的最大计划点；不存在返回 (0,false)。
func dailyPointAtOrBefore(timesOfDay []int64, createdAt, t int64) (int64, bool) {
	if t < createdAt || len(timesOfDay) == 0 {
		return 0, false
	}
	day := dayStart(t)
	for d := day; ; d -= dayLen {
		if d+timesOfDay[len(timesOfDay)-1] < createdAt {
			return 0, false
		}
		for i := len(timesOfDay) - 1; i >= 0; i-- {
			pt := d + timesOfDay[i]
			if pt <= t && pt >= createdAt {
				return pt, true
			}
		}
	}
}

// dailyPointAtOrAfter 返回 >= lb 且不早于 createdAt 的最小计划点。
func dailyPointAtOrAfter(timesOfDay []int64, createdAt, lb int64) (int64, bool) {
	if len(timesOfDay) == 0 {
		return 0, false
	}
	from := max64(lb, createdAt)
	day := dayStart(from)
	for d := day; ; d += dayLen {
		for _, tod := range timesOfDay {
			t := d + tod
			if t >= from {
				return t, true
			}
		}
	}
}

// sortedIntsContains 判断升序切片是否含 v（二分）。
func sortedIntsContains(a []int64, v int64) bool {
	lo, hi := 0, len(a)
	for lo < hi {
		mid := (lo + hi) / 2
		if a[mid] < v {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo < len(a) && a[lo] == v
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

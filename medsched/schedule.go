package medsched

import "sort"

const secondsPerDay int64 = 86400

// FreqKind 是医嘱频次种类。
type FreqKind int

const (
	FreqInterval FreqKind = iota + 1 // 固定间隔
	FreqDaily                        // 固定时点
	FreqPRN                          // 必要时
)

// Frequency 描述医嘱频次。
//   - 固定间隔：First 为首次计划时刻，H 为间隔；
//   - 固定时点：Times 为日内秒数集合（0..86399）；
//   - 必要时：PRNMin 为同医嘱最小间隔，PRNLimit 为滚动 24h 次数上限。
type Frequency struct {
	Kind     FreqKind
	First    int64
	H        int64
	Times    []int64
	PRNMin   int64
	PRNLimit int64
}

// validateFreq 校验频次参数及与窗口/安全间隔的关系。
// openAt 为医嘱开立时刻，minSafe 为药品最小安全给药间隔，W 为窗口半宽。
// validateFreqShape 校验频次自身的形状参数（不依赖药品目录）。
func validateFreqShape(f Frequency, openAt, w int64) error {
	switch f.Kind {
	case FreqInterval:
		if f.H <= 0 {
			return errf(ErrInvalidParam, "固定间隔 H 必须为正整数")
		}
		if f.First < openAt {
			return errf(ErrInvalidParam, "首次计划时刻早于开立时刻")
		}
		if f.H <= 2*w {
			return errf(ErrInvalidParam, "间隔 H 必须大于 2W")
		}
	case FreqDaily:
		if len(f.Times) == 0 {
			return errf(ErrInvalidParam, "固定时点集合为空")
		}
		seen := map[int64]bool{}
		for _, t := range f.Times {
			if t < 0 || t >= secondsPerDay {
				return errf(ErrInvalidParam, "日内时点超出 [0,86399]")
			}
			if seen[t] {
				return errf(ErrInvalidParam, "日内时点重复")
			}
			seen[t] = true
		}
		sorted := append([]int64(nil), f.Times...)
		sortInts64(sorted)
		for i := 0; i < len(sorted); i++ {
			j := (i + 1) % len(sorted)
			gap := sorted[j] - sorted[i]
			if j == 0 {
				gap += secondsPerDay // 跨日首尾
			}
			if gap <= 2*w {
				return errf(ErrInvalidParam, "相邻时点差必须大于 2W")
			}
		}
	case FreqPRN:
		if f.PRNMin <= 0 {
			return errf(ErrInvalidParam, "必要时最小间隔必须为正整数")
		}
		if f.PRNLimit <= 0 {
			return errf(ErrInvalidParam, "必要时次数上限必须为正整数")
		}
	default:
		return errf(ErrInvalidParam, "未知频次种类")
	}
	return nil
}

// validateFreqSafety 校验频次间距不小于药品最小安全给药间隔。
func validateFreqSafety(f Frequency, minSafe int64) error {
	switch f.Kind {
	case FreqInterval:
		if f.H < minSafe {
			return errf(ErrInvalidParam, "间隔 H 小于药品最小安全给药间隔")
		}
	case FreqDaily:
		sorted := sortDailyTimes(f.Times)
		for i := 0; i < len(sorted); i++ {
			j := (i + 1) % len(sorted)
			gap := sorted[j] - sorted[i]
			if j == 0 {
				gap += secondsPerDay
			}
			if gap < minSafe {
				return errf(ErrInvalidParam, "相邻时点差小于药品最小安全给药间隔")
			}
		}
	}
	return nil
}

// intervalPoint 计算固定间隔序列第 k 个计划点（k>=0），锚点为 anchor，步长 H。
func intervalPoint(anchor, h int64, k int64) int64 {
	return anchor + k*h
}

// intervalIndexAt 计算计划时刻不晚于 t 的最后一个计划点下标；全部晚于 t 时返回 -1。
func intervalIndexAt(anchor, h, t int64) int64 {
	if t < anchor {
		return -1
	}
	return (t - anchor) / h
}

// intervalIndexRange 返回计划时刻落在 [lo, hi] 的计划点下标区间 [klo, khi]。
// 无点时返回 klo > khi。
func intervalIndexRange(anchor, h, lo, hi int64) (int64, int64) {
	klo := intervalIndexAt(anchor, h, lo-1) + 1 // 第一个 >= lo
	khi := intervalIndexAt(anchor, h, hi)       // 最后一个 <= hi
	return klo, khi
}

// dailyPointsBetween 枚举固定时点医嘱计划时刻落在 [lo, hi] 的点，按时刻升序回调。
// times 为已排序去重的日内时点。
func dailyPointsBetween(times []int64, lo, hi int64, fn func(t int64)) {
	if lo > hi || len(times) == 0 {
		return
	}
	startDay := floorDiv(lo, secondsPerDay)
	endDay := floorDiv(hi, secondsPerDay)
	for day := startDay; day <= endDay; day++ {
		base := day * secondsPerDay
		for _, tod := range times {
			t := base + tod
			if t >= lo && t <= hi {
				fn(t)
			}
		}
	}
}

// sortDailyTimes 返回排序去重后的日内时点副本（开立时已校验无重复）。
func sortDailyTimes(times []int64) []int64 {
	out := append([]int64(nil), times...)
	sortInts64(out)
	return out
}

// floorDiv 对非负被除数等价于普通整除；保留独立函数以表达“日起点”语义。
func floorDiv(a, b int64) int64 { return a / b }

func sortInts64(xs []int64) { sort.Slice(xs, func(i, j int) bool { return xs[i] < xs[j] }) }

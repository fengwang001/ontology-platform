package meterengine

import "math"

// segmentDelta 计算同一挂接期内相邻两条读数之间的显示差值：
// 后值不小于前值取差值；后值小于前值视为恰好翻转一次，按显示位数补足一圈。
// 返回显示差值（未乘倍率）与翻转次数（0 或 1）。
func segmentDelta(meter *Meter, before, after *Reading) (int64, int) {
	if after.display >= before.display {
		return after.display - before.display, 0
	}
	return after.display + meter.modulus - before.display, 1
}

// mulOverflow 返回 a*b（溢出时第二返回值为 true）。
func mulOverflow(a, b int64) (int64, bool) {
	if a == 0 || b == 0 {
		return 0, false
	}
	p := a * b
	if p/b != a || (p == math.MinInt64 && (a == -1 || b == -1)) {
		return 0, true
	}
	return p, false
}

// validateSegment 检查「区间显示差值 * 倍率」是否超过
// 「单位时间最大合理用电量 * 区间时长」上限；取等通过。
// maxPerUnit <= 0 表示该供电点未设置上限，不做合理性限制。
func validateSegment(meter *Meter, before, after *Reading, maxPerUnit int64) error {
	delta, _ := segmentDelta(meter, before, after)
	used, overflow := mulOverflow(delta, meter.multiplier)
	duration := after.time - before.time
	if maxPerUnit <= 0 {
		return nil
	}
	limit, lov := mulOverflow(maxPerUnit, duration)
	if overflow || lov || used > limit {
		return errUnreasonable("区间 %d..%d 用电量 %d 超过上限 %d",
			before.time, after.time, used, limit)
	}
	return nil
}

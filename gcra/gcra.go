// Package gcra 以通用信元速率算法（GCRA）做纯函数限流判定。
//
// 时间单位毫秒，响应头中的 Reset/RetryAfter 为秒。
// 所有量为 int64：now≤10^15，TAT≤10^15+10^12，cost·T≤10^12，不会溢出。
package gcra

// Decision 是一次 GCRA 判定的完整结果。
type Decision struct {
	Allowed    bool  // 是否放行
	Limit      int64 // 限流响应头 Limit，恒等于 B
	Remaining  int64 // 响应头 Remaining
	Reset      int64 // 响应头 Reset，秒
	RetryAfter int64 // 响应头 RetryAfter，秒；仅被限流时有效
	NewTAT     int64 // 放行时应写入的理论到达时刻；被限流时调用方不得写入
}

// Possible 报告 cost 是否可能满足：cost>B 则永远无法满足（与 TAT 无关）。
func Possible(cost, b int64) bool {
	return cost <= b
}

// Evaluate 按 GCRA 判定一次请求。
// tat 为主体当前理论到达时刻，hasTAT 为 false 表示尚无欠账。
// 纯函数，不修改任何状态；相同输入恒产生相同输出。
func Evaluate(tat int64, hasTAT bool, now, cost, t, b int64) Decision {
	a := now
	if hasTAT && tat > now {
		a = tat
	}
	newTAT := a + cost*t
	burst := b * t
	d := Decision{Limit: b, NewTAT: newTAT}
	if newTAT-now <= burst {
		d.Allowed = true
		d.Remaining = (burst - (newTAT - now)) / t
		d.Reset = ceilSeconds(newTAT - now)
		return d
	}
	d.Remaining = (burst - (a - now)) / t
	if d.Remaining < 0 {
		d.Remaining = 0
	}
	d.Reset = ceilSeconds(a - now)
	d.RetryAfter = ceilSeconds(newTAT - now - burst)
	return d
}

// ceilSeconds 将非负毫秒数向上取整为秒；恰为整秒时不进位。
func ceilSeconds(ms int64) int64 {
	if ms <= 0 {
		return 0
	}
	return (ms + 999) / 1000
}

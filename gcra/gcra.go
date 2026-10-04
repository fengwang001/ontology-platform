// Package gcra 以理论到达时刻（TAT）实现通用信元速率算法。
// 全部为纯函数，不持有状态；时间单位为毫秒。
package gcra

import "errors"

// ErrNever 表示 cost > B，请求永远无法满足（与 TAT 无关）。
var ErrNever = errors.New("gcra: cost exceeds burst, can never be satisfied")

// Outcome 是一次 GCRA 判定的结果。
type Outcome struct {
	Allowed    bool
	A          int64 // max(先前 TAT, now)
	New        int64 // A + cost*T
	Remaining  int64
	Reset      int64 // 秒
	RetryAfter int64 // 秒，仅被限流时有效
}

// ceilSec 计算 ⌈ms/1000⌉，ms ≥ 0。
func ceilSec(ms int64) int64 {
	return (ms + 999) / 1000
}

// Check 判定一次请求。hasTAT 报告 prevTAT 是否已存在。
// 放行时调用方应把 New 存为该主体的新 TAT；被限流时不得改变 TAT。
// 不变量：放行 ⇔ New−now ≤ B×T，故 TAT ≤ 放行时 now + B×T。
func Check(prevTAT int64, hasTAT bool, now, cost, T, B int64) (Outcome, error) {
	if cost > B {
		return Outcome{}, ErrNever
	}
	a := now
	if hasTAT && prevTAT > a {
		a = prevTAT
	}
	newTAT := a + cost*T
	if newTAT-now <= B*T {
		return Outcome{
			Allowed:   true,
			A:         a,
			New:       newTAT,
			Remaining: (B*T - (newTAT - now)) / T,
			Reset:     ceilSec(newTAT - now),
		}, nil
	}
	remaining := (B*T - (a - now)) / T
	if remaining < 0 {
		remaining = 0
	}
	return Outcome{
		Allowed:    false,
		A:          a,
		New:        newTAT,
		Remaining:  remaining,
		Reset:      ceilSec(a - now),
		RetryAfter: ceilSec(newTAT - now - B*T),
	}, nil
}

// Package order 提供事件时间重排所需的纯规则：
// (TS, Seq) 全序比较、迟到判定、单调水位线计算。本包不依赖其他包。
package order

import "math"

// PosInf 是 Flush 使用的正无穷水位线：任何有限 TS 都满足 TS <= PosInf。
const PosInf = int64(math.MaxInt64)

// Key 是主输出的全序键：先按事件时间 TS，再按到达序号 Seq 破平。
type Key struct {
	TS  int64
	Seq int64
}

// Less 报告 a 是否严格先于 b。TS 相等时 Seq 较小（先到）者在前，
// 因而主输出是按到达顺序的稳定排序。
func Less(a, b Key) bool {
	if a.TS != b.TS {
		return a.TS < b.TS
	}
	return a.Seq < b.Seq
}

// Late 用「事件到达之前」的水位线判定迟到：TS <= wm 即迟到。
// 尚无水位线（一个事件都没接受过）时永不迟到。
func Late(ts int64, wm int64, hasWM bool) bool {
	return hasWM && ts <= wm
}

// Advance 返回推进后的水位线 max(wm, ts-delay)，保证只进不退。
// 返回的 ok 表示此刻是否存在有效水位线（接受首个事件后恒为 true）。
func Advance(wm int64, hasWM bool, ts, delay int64) (next int64, nextOK bool) {
	cand := ts - delay
	if !hasWM || cand > wm {
		return cand, true
	}
	return wm, true
}

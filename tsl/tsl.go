// Package tsl 提供时间戳与位点的基础：前缀最大值的增量更新与 (off, ts) 关系。
// 不依赖其他包。
package tsl

// Rec 是一条已追加记录：Off 为严格递增的位点，Ts 为事件时间戳（任意 int64，
// 非单调、可重复、可为负）。
type Rec struct {
	Off int64
	Ts  int64
}

// NextPM 增量维护前缀最大值：已知 pm[o-1]=prev，追加 ts 后返回 pm[o]=max(prev, ts)。
// 因 NextPM(prev, ts) >= prev 恒成立，pm 序列必然非递减。
func NextPM(prev, ts int64) int64 {
	if ts > prev {
		return ts
	}
	return prev
}

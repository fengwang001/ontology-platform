// Package rk 定义单键状态（lastSeen、slot）与 (lastSeen, slot) 序判定。
package rk

// State 是单键状态：LastSeen 为最近一次 Track 的时间戳（可被刷新回退），
// Slot 为该键首次进入蓄存时分配的单调递增、驱逐后不复用的位点。
type State struct {
	LastSeen int64
	Slot     int64
}

// Less 报告 a 在 (LastSeen, Slot) 字典序下是否严格小于 b：
// 先比 LastSeen，并列时较小 Slot（更早首次进入者）更小。
func Less(a, b State) bool {
	if a.LastSeen != b.LastSeen {
		return a.LastSeen < b.LastSeen
	}
	return a.Slot < b.Slot
}

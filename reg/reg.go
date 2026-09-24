// Package reg 是单个键的首写胜出（first-write-wins）去重寄存器：
// 生效值永远是迄今见过的最小 Seq 的写入。不依赖其他包。
package reg

// Reg 保存单个 Key 的寄存器状态：生效值 (Seq, Val) 与是否已物化。
type Reg struct {
	Seq int64
	Val string
	Set bool // 是否已有生效值（已物化）
}

// Apply 处理一条到达的写入 (seq, val)，返回：
//
//	retract: 是否撤回了先前物化的值（此时 oseq/oval 是被撤回的那条，必须原样写进 changelog）
//	won:     本条写入是否成为新的生效值（false 表示落败被去重）
//
// 不变量 3 由这里保证：仅当 seq 严格小于当前生效 Seq 才替换，故生效 Seq 只减不增。
func (r *Reg) Apply(seq int64, val string) (retract bool, oseq int64, oval string, won bool) {
	if !r.Set {
		r.Seq, r.Val, r.Set = seq, val, true
		return false, 0, "", true
	}
	if seq < r.Seq {
		oseq, oval = r.Seq, r.Val
		r.Seq, r.Val = seq, val
		return true, oseq, oval, true
	}
	return false, 0, "", false
}

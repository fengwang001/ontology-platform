// Package delta 实现单 Key 的增量编码：基线 + 追加式 delta 日志 + 求和读取。
// 不依赖其他包。本类型不做并发控制，并发安全由上层 store 保证。
package delta

// Entry 是单个 Key 的累计值表示：val = base + Σ(deltas)。
type Entry struct {
	base   int64
	deltas []int64
}

// Append 把 d 追加到 delta 日志尾部（可为负或零），不触碰 base。
// 纯尾部追加，不访问任何已有 delta 条目，O(1)。
func (e *Entry) Append(d int64) {
	e.deltas = append(e.deltas, d)
}

// Sum 返回 base + 按追加顺序累加的全部未合并 delta；空日志时 Σ=0。
func (e *Entry) Sum() int64 {
	v := e.base
	for _, d := range e.deltas {
		v += d
	}
	return v
}

// Compact 把全部未合并 delta 合并进 base（base += Σ），随后清空日志。
// 合并前后 Sum() 不变：合并前 base+Σ，合并后 (base+Σ)+0。
func (e *Entry) Compact() {
	if len(e.deltas) == 0 {
		return
	}
	e.base = e.Sum()
	e.deltas = nil
}

// Pending 返回当前未合并的 delta 条目数。
func (e *Entry) Pending() int {
	return len(e.deltas)
}

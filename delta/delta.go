// Package delta 实现单 Key 的基线 + 追加 delta 日志 + 求和读取。
// 不依赖任何其他包。
package delta

// Log 是一个 Key 的累计值：可见值恒为 base + Σ(pending)。
type Log struct {
	base    int64   // 已合并进基线的部分
	pending []int64 // 未合并的 delta 追加日志，按追加顺序
}

// Append 把 d 追加到日志尾部（可为负或零），不触碰 base。O(1) 纯尾部追加。
func (l *Log) Append(d int64) {
	l.pending = append(l.pending, d)
}

// Value 返回 base + Σ(pending)，按追加顺序累加；空日志时 Σ=0。
func (l *Log) Value() int64 {
	return l.base + l.Sum()
}

// Sum 返回未合并 delta 之和。
func (l *Log) Sum() int64 {
	var s int64
	for _, d := range l.pending {
		s += d
	}
	return s
}

// Merge 把全部未合并 delta 合并进基线并清空日志；可见值不变。
func (l *Log) Merge() {
	if len(l.pending) == 0 {
		return
	}
	l.base += l.Sum()
	l.pending = l.pending[:0]
}

// Len 返回当前未合并 delta 条目数。
func (l *Log) Len() int {
	return len(l.pending)
}

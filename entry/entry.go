// Package entry 定义日志条目与日志的基本操作，不依赖其他包。
package entry

// Entry 是一条日志条目，Index 从 1 起连续。
type Entry struct {
	Term  int
	Index int
	Cmd   string
}

// At 返回 log 中 1 基 index 处的条目；index 越界（<1 或 >len(log)）时 ok=false。
func At(log []Entry, index int) (e Entry, ok bool) {
	if index < 1 || index > len(log) {
		return Entry{}, false
	}
	return log[index-1], true
}

// Match 判定 log 在 prevIndex 处的条目 term 是否等于 prevTerm。
// prevIndex==0 表示「没有前一条」，恒为匹配。
func Match(log []Entry, prevIndex, prevTerm int) bool {
	if prevIndex == 0 {
		return true
	}
	e, ok := At(log, prevIndex)
	return ok && e.Term == prevTerm
}

// Truncate 把 log 截断到 prevIndex，丢弃 prevIndex+1 起的冲突后缀。
func Truncate(log []Entry, prevIndex int) []Entry {
	if prevIndex >= len(log) {
		return log
	}
	return log[:prevIndex]
}

// Append 把 es 依次追加到 log 末尾（调用方保证 index 连续）。
func Append(log []Entry, es ...Entry) []Entry {
	return append(log, es...)
}

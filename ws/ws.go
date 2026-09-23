// Package ws 延迟判定行尾空白（空格与制表符）。
//
// 一串空白只有看到行尾或流结束才知道它是否位于行尾：在此之前只能缓冲，
// 不能提前输出（万一是行尾就要删除），也不能提前丢弃（万一是行内空白）。
package ws

// Tracker 累积当前“自最后一个非空白字节以来”的空白串。零值即可用。
type Tracker struct {
	buf   []byte
	start int
}

// IsSpace 报告 b 是否为行尾空白（空格或制表符）。
func IsSpace(b byte) bool { return b == ' ' || b == '\t' }

// Add 把空白字节 b（原文偏移 pos）追加到待定串。首个空白记录其起点。
func (t *Tracker) Add(b byte, pos int) {
	if len(t.buf) == 0 {
		t.start = pos
	}
	t.buf = append(t.buf, b)
}

// Pending 报告是否有待定空白串。
func (t *Tracker) Pending() bool { return len(t.buf) > 0 }

// Start 返回待定空白串的原文起始偏移（无待定时无意义）。
func (t *Tracker) Start() int { return t.start }

// Len 返回待定空白字节数。
func (t *Tracker) Len() int { return len(t.buf) }

// Flush 返回待定空白副本并清空：它被定案为行内空白，需原样输出。
func (t *Tracker) Flush() []byte {
	out := append([]byte(nil), t.buf...)
	t.buf = t.buf[:0]
	return out
}

// Drop 清空待定空白：它被定案为行尾空白，需删除。返回其原文 [start,end)。
func (t *Tracker) Drop() (start, end int) {
	start, end = t.start, t.start+len(t.buf)
	t.buf = t.buf[:0]
	return start, end
}

// Range 返回待定空白的原文 [start,end)（不清空）。
func (t *Tracker) Range() (start, end int) {
	return t.start, t.start + len(t.buf)
}

// Bytes 返回待定空白副本（不清空）。
func (t *Tracker) Bytes() []byte { return append([]byte(nil), t.buf...) }

// Reset 回到零值状态。
func (t *Tracker) Reset() { t.buf = t.buf[:0] }

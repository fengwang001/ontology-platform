// Package ws 延迟判定行尾空白：只有看到行尾或流结束，才知道一串空格/制表符
// 是否位于行尾。判定前的字节必须缓冲，不能提前输出或丢弃。
package ws

// IsWS 报告 b 是否为参与判定的空白（空格或制表符）。
func IsWS(b byte) bool { return b == ' ' || b == '\t' }

// Buffer 累积一段连续的、尚未判定的空白，并记录其起始原文偏移。
type Buffer struct {
	start int
	data  []byte
}

// Begin 在原文偏移 off 处开始一串新空白。
func (b *Buffer) Begin(off int) {
	b.start = off
	b.data = b.data[:0]
}

// Add 追加一个空白字节。
func (b *Buffer) Add(c byte) { b.data = append(b.data, c) }

// Active 报告当前是否正处于未决空白串中。
func (b *Buffer) Active() bool { return len(b.data) > 0 }

// Start 返回当前空白串的起始原文偏移。
func (b *Buffer) Start() int { return b.start }

// Len 返回缓冲字节数。
func (b *Buffer) Len() int { return len(b.data) }

// Bytes 返回缓冲内容（只读）。
func (b *Buffer) Bytes() []byte { return b.data }

// Reset 清空未决空白串。
func (b *Buffer) Reset() { b.data = b.data[:0] }

// Detach 取出缓冲字节并清空（用于强制按行中间空白重新输出）。
func (b *Buffer) Detach() []byte {
	out := append([]byte(nil), b.data...)
	b.data = b.data[:0]
	return out
}

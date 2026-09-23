// Package ws 延迟判定行尾空白：只有看到行尾或流结束，才知道一串空格/制表符是否在行尾。
package ws

// IsTrailingByte 报告字节是否属于可被行尾裁剪的空白（空格或制表符）。
func IsTrailingByte(b byte) bool { return b == ' ' || b == '\t' }

// Buffer 累积当前行内、自最近一个非空白字节之后连续的空白。
// 它不知道这些空白是否在行尾：遇行尾则丢弃，遇非空白则原样吐出。
type Buffer struct {
	buf []byte
}

// Add 追加一个空白字节。
func (b *Buffer) Add(c byte) { b.buf = append(b.buf, c) }

// Len 返回已累积的待定空白字节数。
func (b *Buffer) Len() int { return len(b.buf) }

// Pending 报告是否有待定空白。
func (b *Buffer) Pending() bool { return len(b.buf) > 0 }

// Take 取出待定空白并清空（判定为「不在行尾」，原样输出时使用）。
func (b *Buffer) Take() []byte {
	out := b.buf
	b.buf = nil
	return out
}

// Drop 丢弃待定空白（判定为「在行尾」时使用）并返回丢弃的字节数。
func (b *Buffer) Drop() int {
	n := len(b.buf)
	b.buf = nil
	return n
}

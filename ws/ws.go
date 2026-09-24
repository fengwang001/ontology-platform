// Package ws 维护一串「是否属于行尾空白」尚未判定的空格/制表符。
// 只有看到行尾或流结束才能判定：行尾则丢弃，否则原样吐出。
package ws

// IsWhitespace 报告 b 是否为参与行尾判定的空白（空格或制表符）。
func IsWhitespace(b byte) bool { return b == ' ' || b == '\t' }

// Buffer 是延迟判定的空白缓冲。零值可用，非并发安全。
type Buffer struct {
	buf []byte
}

// Add 追加一个空白字节（调用方应先用 IsWhitespace 确认）。
func (b *Buffer) Add(c byte) { b.buf = append(b.buf, c) }

// Len 返回当前缓冲的空白字节数。
func (b *Buffer) Len() int { return len(b.buf) }

// Pending 返回缓冲内容的只读视图（在下一次 Take/Drop/Reset 前有效）。
func (b *Buffer) Pending() []byte { return b.buf }

// Take 取出缓冲的空白并清空：用于判定为「非行尾空白」，原样输出。
func (b *Buffer) Take() []byte {
	out := b.buf
	b.buf = nil
	return out
}

// Drop 丢弃缓冲的全部空白：用于判定为「行尾空白」。
func (b *Buffer) Drop() { b.buf = nil }

// Reset 复位到零值状态。
func (b *Buffer) Reset() { b.buf = nil }

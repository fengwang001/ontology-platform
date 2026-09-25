// Package ws 处理行尾空白（空格与制表符）的延迟判定与缓冲。
//
// 一串空白在看到行尾或流结束之前，无法知道它是否位于行尾：
// 其后若出现普通字节，它是行内空白，必须原样输出；
// 其后若是行尾（或流在空白处结束），它是行尾空白，必须删除。
// 因此判定只能由驱动方在拿到后续上下文时调用 Decide。
package ws

// IsSpace 报告字节是否为行尾空白的候选（空格或制表符）。
func IsSpace(b byte) bool { return b == ' ' || b == '\t' }

// Pending 是一串尚未判定的空白的缓冲。
type Pending struct {
	buf []byte
}

// Add 追加一个空白字节。
func (p *Pending) Add(b byte) { p.buf = append(p.buf, b) }

// Len 返回待判定空白字节数。
func (p *Pending) Len() int { return len(p.buf) }

// Bytes 返回缓冲内容（只读，下次 Reset/Add 前有效）。
func (p *Pending) Bytes() []byte { return p.buf }

// Drain 返回缓冲内容并清空，用于“判定为行内空白，原样输出”。
func (p *Pending) Drain() []byte {
	out := p.buf
	p.buf = nil
	return out
}

// Reset 清空缓冲，用于“判定为行尾空白，删除”。
func (p *Pending) Reset() { p.buf = nil }

// Decide 在拿到后续上下文时给出判定。
// followedByLineEnd 为 true 表示空白之后是行尾（或流在空白处结束）：
// 该串是行尾空白；否则是行内空白，应原样保留。
func Decide(p *Pending, followedByLineEnd bool) (trailing bool) {
	if p.Len() == 0 {
		return false
	}
	return followedByLineEnd
}

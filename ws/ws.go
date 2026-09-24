// Package ws 延迟判定一串空格/制表符是否为行尾空白：
// 只有看到行尾或流结束才知道，期间必须缓冲，不能提前输出或丢弃。
package ws

// IsSpace 报告字节是否是参与行尾空白判定的空格或制表符。
func IsSpace(b byte) bool { return b == ' ' || b == '\t' }

// Run 缓冲一串尚未判定的连续空格/制表符。
type Run struct {
	buf []byte
}

// Len 是当前缓冲的空白字节数。
func (r *Run) Len() int { return len(r.buf) }

// Bytes 返回缓冲内容（只读语义，调用方不得修改）。
func (r *Run) Bytes() []byte { return r.buf }

// Add 追加一个空白字节。
func (r *Run) Add(b byte) { r.buf = append(r.buf, b) }

// Active 报告当前是否处在一串未判定空白中。
func (r *Run) Active() bool { return len(r.buf) > 0 }

// Keep 判定这串空白不是行尾空白（普通内容到达），返回并清空缓冲。
func (r *Run) Keep() []byte {
	b := r.buf
	r.buf = nil
	return b
}

// Drop 判定这串空白是行尾空白（行尾到达或流结束且本行无后续内容），清空缓冲。
func (r *Run) Drop() { r.buf = r.buf[:0] }

// Reset 无条件清空。
func (r *Run) Reset() { r.buf = nil }

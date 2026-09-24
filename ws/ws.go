// Package ws 延迟判定行尾空白：只有看到行尾或流结束，
// 才知道一串空格/制表符是否位于行尾。
package ws

// IsSpace 报告字节是否是受管的行尾空白（空格或制表符）。
func IsSpace(b byte) bool { return b == ' ' || b == '\t' }

// Run 记录一串尚未定性的连续空白。
// 起点在原文中的偏移由调用方保存；Run 自身只持有空白字节。
type Run struct {
	buf []byte
}

// Add 追加一个空白字节，返回缓冲后的长度。
func (r *Run) Add(b byte) int {
	r.buf = append(r.buf, b)
	return len(r.buf)
}

// Len 返回当前缓冲的空白字节数。
func (r *Run) Len() int { return len(r.buf) }

// Bytes 返回缓冲的空白内容。
func (r *Run) Bytes() []byte { return r.buf }

// Reset 清空待定空白串。
func (r *Run) Reset() { r.buf = r.buf[:0] }

// Move 把缓冲内容交给 dst（零拷贝移交）并清空自身。
// 调用方保证此前定性为"非行尾"，需要原样输出。
func (r *Run) Move(dst *[]byte) {
	*dst = append(*dst, r.buf...)
	r.Reset()
}

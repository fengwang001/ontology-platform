// Package ws 延迟判定行尾空白：只有看到行尾或流结束，才知道一串空格/制表符是否在行尾。
package ws

// IsSpace 报告字节是否为行内空白（空格或制表符）。其它字节（含非法 UTF-8）原样通过。
func IsSpace(b byte) bool { return b == ' ' || b == '\t' }

// Run 记录一串尚未判定的连续空格/制表符。
type Run struct {
	// Bytes 是缓冲下来的空白字节。
	Bytes []byte
	// Start 是这串空白在原文中的起始偏移。
	Start int
}

// Add 追加一个空白字节，pos 是它的原文偏移。
func (r *Run) Add(b byte, pos int) {
	if len(r.Bytes) == 0 {
		r.Start = pos
	}
	r.Bytes = append(r.Bytes, b)
}

// Len 返回缓冲字节数。
func (r *Run) Len() int { return len(r.Bytes) }

// End 返回这串空白之后的原文偏移。
func (r *Run) End() int { return r.Start + len(r.Bytes) }

// Reset 丢弃这串空白（无论最终是输出还是删除，判定后都应清空）。
func (r *Run) Reset() {
	r.Bytes = r.Bytes[:0]
	r.Start = 0
}

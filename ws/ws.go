// Package ws 处理行尾空白（空格与制表符）的延迟判定：
// 一串空白只有在看到行尾或流结束时才知道是否位于行尾。
package ws

// IsSpace 报告 b 是否是参与行尾裁剪的空白：仅空格与制表符。
// 其他 Unicode 空白（如 0xC2 0xA0）原样通过，不在本包职责内。
func IsSpace(b byte) bool {
	return b == ' ' || b == '\t'
}

// Run 是一串连续待定空白的缓冲。空白不能提前输出（在行尾要删除），
// 也不能提前丢弃（在行中间要原样保留），因此必须整串缓冲直到判定点。
type Run struct {
	buf   []byte
	start int // 该串第一个字节的原文偏移
}

// NewRun 创建记录起始原文偏移的空白缓冲。
func NewRun(startOffset int) *Run {
	return &Run{start: startOffset}
}

// Add 追加一个空白字节。
func (r *Run) Add(b byte) {
	r.buf = append(r.buf, b)
}

// Len 返回缓冲的空白字节数。
func (r *Run) Len() int { return len(r.buf) }

// Start 返回该串首字节的原文偏移。
func (r *Run) Start() int { return r.start }

// Bytes 返回缓冲的空白内容（行中间判定时原样输出）。
func (r *Run) Bytes() []byte { return r.buf }

// Reset 清空缓冲，准备记录下一串空白。
func (r *Run) Reset(startOffset int) {
	r.buf = r.buf[:0]
	r.start = startOffset
}

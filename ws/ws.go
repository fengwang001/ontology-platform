// Package ws 处理行尾空白（空格与制表符）的延迟判定。
//
// 一串空白在行尾还是行中间，只有看到其后的行尾或流结束才能确定：
//   - 空白后出现行尾 → 整串删除（行尾空白）；
//   - 空白后出现普通字节（含下一行的内容/空串）→ 整串原样输出（行中间空白）。
//
// 因此缓冲必须先攒起这串未决空白，不能提前输出或丢弃。本包不依赖其他包。
package ws

// IsSpace 报告 b 是否为受管的行尾空白：仅空格与制表符。
func IsSpace(b byte) bool { return b == ' ' || b == '\t' }

// Buffer 是一串尚未判定的尾部空白。
type Buffer struct {
	b     []byte
	limit int // 最大容量；0 表示不限
}

// New 创建缓冲，limit 为允许缓冲的空白字节上限（0 表示不限）。
func New(limit int) *Buffer { return &Buffer{limit: limit} }

// Add 追加一个空白字节。容量超限时返回 false（调用方据此拒绝并进入终态）。
func (b *Buffer) Add(c byte) bool {
	if b.limit > 0 && len(b.b) >= b.limit {
		return false
	}
	b.b = append(b.b, c)
	return true
}

// Pending 返回当前缓冲的空白字节（不清除）。
func (b *Buffer) Pending() []byte { return b.b }

// Len 返回待判定空白字节数。
func (b *Buffer) Len() int { return len(b.b) }

// Keep 判定整串为行中间空白：返回其字节并清空。
func (b *Buffer) Keep() []byte {
	out := b.b
	b.b = nil
	return out
}

// Trim 判定整串为行尾空白：丢弃并清空。
func (b *Buffer) Trim() { b.b = nil }

// Reset 清空缓冲。
func (b *Buffer) Reset() { b.b = nil }

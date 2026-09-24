// Package ws 延迟判定空格与制表符是否位于行尾：只有看到行尾或流结束才知道。
package ws

// IsSpace 报告字节是否为可被行尾裁剪的空白（仅空格与制表符）。
func IsSpace(b byte) bool { return b == ' ' || b == '\t' }

// Buf 保存最近一段尚未判定的连续空白。零值即可使用。
// 不变量：缓冲非空时，它紧跟在已输出内容之后；缓冲之前不允许有待定 \r
// （norm 保证先解决待定 \r 再累积空白）。
type Buf struct {
	data []byte
}

// Add 追加一个已知为空白的字节（调用方应先用 IsSpace 判断）。
func (b *Buf) Add(c byte) { b.data = append(b.data, c) }

// Len 返回当前待判定空白字节数。
func (b *Buf) Len() int { return len(b.data) }

// Pending 返回待判定空白的拷贝（不改变状态）。
func (b *Buf) Pending() []byte { return b.data }

// Keep 判定这些空白不是行尾空白：返回缓冲内容并清空，交给调用方原样输出。
func (b *Buf) Keep() []byte {
	out := b.data
	b.data = nil
	return out
}

// Drop 判定这些空白位于行尾：丢弃并清空（对应映射上的删除段）。
func (b *Buf) Drop() { b.data = nil }

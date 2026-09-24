// Package ws 做行尾空白（空格与制表符）的延迟判定：
// 只有看到行尾或流结束，才知道一串空白是否位于行尾。
package ws

// IsTrailingByte 报告字节是否参与行尾空白判定（空格或制表符）。
func IsTrailingByte(b byte) bool { return b == ' ' || b == '\t' }

// Tracker 累积当前行尾尚未定性的连续空白。非并发安全。
type Tracker struct {
	// run 是自上一个确定字节起累积的空白字节；len 即待定数量。
	run []byte
}

// Add 追加一个已确认的空白字节，返回当前待定空白长度。
func (t *Tracker) Add(b byte) int {
	t.run = append(t.run, b)
	return len(t.run)
}

// Pending 返回待定空白字节数（不修改状态）。
func (t *Tracker) Pending() int { return len(t.run) }

// Run 返回待定空白的拷贝（供「实为行中空白」时原样输出）。
func (t *Tracker) Run() []byte {
	out := make([]byte, len(t.run))
	copy(out, t.run)
	return out
}

// Resolve 在遇到行尾时调用：空白位于行尾，清空并丢弃。
func (t *Tracker) Resolve() { t.run = t.run[:0] }

// Reset 在遇到非空白内容字节时调用：空白位于行中，
// 返回此前累积的空白字节（应原样输出）并清空。
func (t *Tracker) Reset() []byte {
	out := t.Run()
	t.run = t.run[:0]
	return out
}

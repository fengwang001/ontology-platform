// Package ws 延迟判定一串空格/制表符是否位于行尾：
// 只有看到行尾或流结束时才知道该缓冲（删除）还是吐出（行中空白）。
package ws

// IsSpace 报告 b 是否为参与行尾空白判定的空格或制表符。
func IsSpace(b byte) bool { return b == ' ' || b == '\t' }

// Tracker 维护当前行内“最后一个非空白字节之后”的待定空白串。
// 它只负责分类，不负责 I/O：待定字节由上层缓冲，Flush 返回其长度。
type Tracker struct {
	pendingLen int
}

// Reset 回到初始状态。
func (t *Tracker) Reset() { t.pendingLen = 0 }

// Pending 返回当前待定空白的字节数。
func (t *Tracker) Pending() int { return t.pendingLen }

// Space 记录一个空白字节：它加入待定串。
func (t *Tracker) Space() { t.pendingLen++ }

// Content 记录一个非空白、非行尾字节：此前待定的空白属于行中空白，
// 返回被“释放”（必须原样输出）的空白数，计数清零后重新累计。
func (t *Tracker) Content() int {
	n := t.pendingLen
	t.pendingLen = 0
	return n
}

// LineEnd 记录一个行尾：待定空白位于行尾、必须删除。返回被丢弃的字节数。
func (t *Tracker) LineEnd() int {
	n := t.pendingLen
	t.pendingLen = 0
	return n
}

// Flush 在流结束时调用：EOF 等价于行尾，待定空白按行尾空白删除。
func (t *Tracker) Flush() int {
	n := t.pendingLen
	t.pendingLen = 0
	return n
}

// Package ws 处理行尾空白（空格与制表符）的延迟判定：
// 只有看到行尾或流结束，才知道一串空白是否位于行尾。
package ws

// Tracker 累积当前行内最近一串连续空白。非空白字节（且非行尾）到达时，
// 该串被判定为「行中空白」，必须原样输出；行尾到达时则整串删除。
// 单实例非并发安全。
type Tracker struct {
	buf  []byte // 待定空白，其前可能已有定稿内容在外部输出缓冲中
	span int    // 本行已见的待定空白起点前的字节数（信息字段，可用于统计）
}

// IsSpace 报告 b 是否为参与判定的空白（空格或制表符）。
func IsSpace(b byte) bool { return b == ' ' || b == '\t' }

// AddSpace 追加一个空白字节，返回缓冲后的空白串长度。
func (t *Tracker) AddSpace(b byte) int {
	t.buf = append(t.buf, b)
	return len(t.buf)
}

// Pending 返回当前待定的空白串（只读，不应修改）。
func (t *Tracker) Pending() []byte { return t.buf }

// Len 返回待定空白串长度。
func (t *Tracker) Len() int { return len(t.buf) }

// Commit 在遇到「非空白、非行尾」字节时调用：待定空白被判定为行中空白。
// 返回应原样补发的空白串并清空状态。
func (t *Tracker) Commit() []byte {
	out := t.buf
	t.buf = nil
	t.span = 0
	return out
}

// Trim 在行尾或流结束时调用：待定空白被判定为行尾空白并删除。
// 返回被删除的空白串长度并清空状态。
func (t *Tracker) Trim() int {
	n := len(t.buf)
	t.buf = nil
	t.span = 0
	return n
}

// Reset 清空全部状态。
func (t *Tracker) Reset() {
	t.buf = nil
	t.span = 0
}

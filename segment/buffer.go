// Package segment 在一段可注入的字节缓冲（"磁盘"）上提供
// 记录的追加与顺序扫描，依赖 codec 包做记录编解码。
package segment

// Buffer 是可注入的字节缓冲抽象，充当 WAL 的"磁盘"。
// 实现方负责自身的并发安全；segment 只按顺序调用。
type Buffer interface {
	// Len 返回当前已写入的字节数。
	Len() int
	// Append 把 p 原样追加到缓冲末尾，不得修改已有字节。
	Append(p []byte)
	// Bytes 返回缓冲内容的只读视图（不得被调用方修改）。
	Bytes() []byte
}

// MemBuffer 是 Buffer 的纯内存实现，不碰真实文件系统。
// 它不是并发安全的，并发保护由上层的 wal 负责。
type MemBuffer struct {
	data []byte
}

// NewMemBuffer 创建一个空的内存缓冲。
func NewMemBuffer() *MemBuffer {
	return &MemBuffer{}
}

// Len 实现 Buffer。
func (m *MemBuffer) Len() int { return len(m.data) }

// Append 实现 Buffer；append 只会增长底层切片，绝不改写已有字节。
func (m *MemBuffer) Append(p []byte) { m.data = append(m.data, p...) }

// Bytes 实现 Buffer。
func (m *MemBuffer) Bytes() []byte { return m.data }

// Snapshot 返回缓冲内容的独立副本，调用方可自由持有与比较。
func (m *MemBuffer) Snapshot() []byte {
	out := make([]byte, len(m.data))
	copy(out, m.data)
	return out
}

// Truncate 把缓冲截断到前 n 个字节，用于在测试中模拟
// 崩溃导致的任意字节处截断。n 超过当前长度时截到当前长度。
func (m *MemBuffer) Truncate(n int) {
	if n < 0 {
		n = 0
	}
	if n > len(m.data) {
		n = len(m.data)
	}
	m.data = m.data[:n]
}

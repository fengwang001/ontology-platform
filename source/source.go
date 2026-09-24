// Package source 定义字节源抽象，并提供一个可注入故障的内存实现，
// 用于模拟短读、读错误、读到一半长度变化等数据源行为。
// 本包不依赖工程内其他包。
package source

import "io"

// Source 是按偏移读取的字节源。实现允许短读（返回 n < len(p) 且 err == nil），
// 调用方必须循环补齐；到达末尾返回 io.EOF。
type Source interface {
	Size() int64
	ReadAt(p []byte, off int64) (n int, err error)
}

// Mem 是基于 []byte 的 Source，支持注入短读、读错误与长度变化。
// 零值即可用（无故障注入）。Mem 不是并发安全的，每个 goroutine 应持有独立实例。
type Mem struct {
	Data []byte
	// MaxChunk > 0 时，单次 ReadAt 最多返回 MaxChunk 字节（短读注入）。
	MaxChunk int
	// Err 非 nil 时，累计读出 ErrAfter 字节后开始返回该错误（读错误注入）。
	Err      error
	ErrAfter int
	// SizeFunc 非 nil 时，Size() 返回 SizeFunc(真实长度)，
	// 用于模拟"读到一半长度变化"：报告长度与真实数据不一致。
	SizeFunc func(actual int64) int64

	served int
}

// Size 返回资源总长；设置了 SizeFunc 时返回其映射值。
func (m *Mem) Size() int64 {
	if m.SizeFunc != nil {
		return m.SizeFunc(int64(len(m.Data)))
	}
	return int64(len(m.Data))
}

// ReadAt 从 off 处读取最多 len(p) 字节；可能短读。off 越界返回 io.EOF。
func (m *Mem) ReadAt(p []byte, off int64) (int, error) {
	if m.Err != nil && m.served >= m.ErrAfter {
		return 0, m.Err
	}
	if off < 0 || off >= int64(len(m.Data)) {
		return 0, io.EOF
	}
	n := len(m.Data) - int(off)
	if n > len(p) {
		n = len(p)
	}
	if m.MaxChunk > 0 && n > m.MaxChunk {
		n = m.MaxChunk
	}
	copy(p, m.Data[int(off):int(off)+n])
	m.served += n
	return n, nil
}

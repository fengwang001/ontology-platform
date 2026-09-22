package source

import "io"

// Memory 是一个进程内字节源，长度等于底层切片长度。
type Memory struct {
	data []byte
}

// NewMemory 用给定字节创建内存字节源。
func NewMemory(data []byte) *Memory { return &Memory{data: data} }

// Length 返回数据长度。
func (m *Memory) Length() int64 { return int64(len(m.data)) }

// ReadAt 总是尝试读满；越界时返回可读部分并以 io.EOF 表示不足。
func (m *Memory) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, &offsetError{off: off}
	}
	if off >= int64(len(m.data)) {
		return 0, io.EOF
	}
	n := copy(p, m.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

type offsetError struct{ off int64 }

func (e *offsetError) Error() string { return "source: negative offset" }

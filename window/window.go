package window

import "errors"

// ErrBadConfig 在容量为 0 时由 New 返回。
var ErrBadConfig = errors.New("window: capacity must be positive")

// Window 是固定容量的环形字节缓冲，保存最近的历史字节。
// Byte(d) 返回距离当前末尾 d 个位置的字节（d 从 1 开始）。
type Window struct {
	buf  []byte
	mask int
	pos  int64 // 已写入的总字节数
}

// New 创建容量向上取整为 2 的幂（最小 256）的窗口。
func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrBadConfig
	}
	size := 256
	for size < capacity {
		size <<= 1
	}
	return &Window{buf: make([]byte, size), mask: size - 1}, nil
}

// Cap 返回逻辑容量（环形数组大小）。
func (w *Window) Cap() int { return len(w.buf) }

// Len 返回当前保存的字节数。
func (w *Window) Len() int64 {
	if w.pos < int64(len(w.buf)) {
		return w.pos
	}
	return int64(len(w.buf))
}

// Append 追加一个字节。
func (w *Window) Append(b byte) {
	w.buf[w.pos&int64(w.mask)] = b
	w.pos++
}

// Byte 返回距末尾 d 位置的字节，d>=1，要求 d<=Len()。
func (w *Window) Byte(d int) byte {
	return w.buf[(w.pos-int64(d))&int64(w.mask)]
}

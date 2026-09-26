// Package window 实现固定容量的滑动窗口（环形缓冲），支持按距离取回历史字节。
// 不依赖工程内其他包。单个实例不是并发安全的。
package window

import "errors"

// ErrBadCapacity 表示窗口容量非法（必须为正）。
var ErrBadCapacity = errors.New("window: 容量必须为正整数")

// Window 是固定容量的字节环形缓冲，距离 1 表示最近写入的字节。
type Window struct {
	buf []byte
	pos int // 下一个写入位置
	len int
}

// New 创建容量为 cap 的窗口，cap <= 0 时返回 ErrBadCapacity。
func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrBadCapacity
	}
	return &Window{buf: make([]byte, capacity)}, nil
}

// Cap 返回窗口容量。
func (w *Window) Cap() int { return len(w.buf) }

// Len 返回窗口中当前保留的字节数。
func (w *Window) Len() int { return w.len }

// Write 写入一个字节，容量满时覆盖最旧字节。
func (w *Window) Write(b byte) {
	w.buf[w.pos] = b
	w.pos = (w.pos + 1) % len(w.buf)
	if w.len < len(w.buf) {
		w.len++
	}
}

// WriteAll 依次写入一段字节。
func (w *Window) WriteAll(p []byte) {
	for _, b := range p {
		w.Write(b)
	}
}

// At 按距离取回历史字节，dist 取值 [1, Len]，1 为最近写入的字节。
func (w *Window) At(dist int) byte {
	i := w.pos - dist
	i %= len(w.buf)
	if i < 0 {
		i += len(w.buf)
	}
	return w.buf[i]
}

// Package window 实现固定容量的环形滑动窗口，支持按距离取回
// 历史字节（距离 1 为最近写入的字节）。不依赖其他包。
package window

import "errors"

// ErrBadCap 表示窗口容量非法（<= 0）。
var ErrBadCap = errors.New("window: capacity must be positive")

// Window 是固定容量的环形缓冲。容量满后最旧的字节被覆盖。
type Window struct {
	buf []byte
	pos int // 下一个写入位置
	n   int // 累计写入字节数（可超过容量）
}

// New 创建容量为 cap 的窗口；cap <= 0 时返回 ErrBadCap。
func New(cap int) (*Window, error) {
	if cap <= 0 {
		return nil, ErrBadCap
	}
	return &Window{buf: make([]byte, cap)}, nil
}

// Cap 返回窗口容量。
func (w *Window) Cap() int { return len(w.buf) }

// Len 返回累计写入的字节数。
func (w *Window) Len() int { return w.n }

// Write 写入一个字节。
func (w *Window) Write(b byte) {
	w.buf[w.pos] = b
	w.pos = (w.pos + 1) % len(w.buf)
	w.n++
}

// At 按距离取回历史字节：dist=1 是最近写入的字节。
// dist 超出容量或超过已写入字节数时返回 ok=false。
func (w *Window) At(dist int) (b byte, ok bool) {
	if dist < 1 || dist > len(w.buf) || dist > w.n {
		return 0, false
	}
	i := w.pos - dist
	if i < 0 {
		i += len(w.buf)
	}
	return w.buf[i], true
}

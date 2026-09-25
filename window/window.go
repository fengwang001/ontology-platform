// Package window 实现固定容量的环形滑动窗口，支持按距离取回历史字节。
package window

import "errors"

// ErrZeroCapacity 表示窗口容量非法（为 0 或负数）。
var ErrZeroCapacity = errors.New("window: capacity must be positive")

// Window 是固定容量的环形缓冲，保存最近写入的至多 Cap 个字节。
type Window struct {
	buf   []byte
	start int // 最老字节的下标
	n     int // 当前保存的字节数
	total int // 历史写入总字节数
}

// New 构造容量为 capacity 的窗口；capacity <= 0 时拒绝。
func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrZeroCapacity
	}
	return &Window{buf: make([]byte, capacity)}, nil
}

// Cap 返回窗口容量。
func (w *Window) Cap() int { return len(w.buf) }

// Len 返回当前保存的字节数（不超过 Cap）。
func (w *Window) Len() int { return w.n }

// Total 返回历史写入的总字节数。
func (w *Window) Total() int { return w.total }

// Append 写入一个字节，窗口满时挤出最老字节。
func (w *Window) Append(b byte) {
	if w.n < len(w.buf) {
		w.buf[(w.start+w.n)%len(w.buf)] = b
		w.n++
	} else {
		w.buf[w.start] = b
		w.start = (w.start + 1) % len(w.buf)
	}
	w.total++
}

// Byte 按距离取回历史字节：dist=1 是最近写入的字节。
// 调用方保证 1 <= dist <= Len()。
func (w *Window) Byte(dist int) byte {
	i := (w.start + w.n - dist) % len(w.buf)
	if i < 0 {
		i += len(w.buf)
	}
	return w.buf[i]
}

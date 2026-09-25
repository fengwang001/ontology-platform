// Package window 实现固定容量的环形滑动窗口，按距离取回历史字节。
package window

import "errors"

// ErrInvalidConfig 在窗口容量为 0 时返回。
var ErrInvalidConfig = errors.New("window: capacity must be positive")

// Window 是保留最近 cap 个字节的环形缓冲。
// 距离 1 指向最近写入的字节。
type Window struct {
	buf  []byte
	next int // 下一个写入位置
	size int // 已保存字节数（<= cap(buf)）
}

// New 创建容量为 cap 的窗口。
func New(cap int) (*Window, error) {
	if cap <= 0 {
		return nil, ErrInvalidConfig
	}
	return &Window{buf: make([]byte, cap)}, nil
}

// Cap 返回窗口容量。
func (w *Window) Cap() int { return len(w.buf) }

// Len 返回当前保存的历史字节数。
func (w *Window) Len() int { return w.size }

// Add 追加一个字节，最旧的字节被挤出。
func (w *Window) Add(b byte) {
	w.buf[w.next] = b
	w.next++
	if w.next == len(w.buf) {
		w.next = 0
	}
	if w.size < len(w.buf) {
		w.size++
	}
}

// At 返回距离最近写入第 distance 个字节，distance 从 1 开始。
// distance 超出当前历史长度时返回 false。
func (w *Window) At(distance int) (byte, bool) {
	if distance <= 0 || distance > w.size {
		return 0, false
	}
	idx := w.next - distance
	if idx < 0 {
		idx += len(w.buf)
	}
	return w.buf[idx], true
}

// Reset 清空历史（容量不变）。
func (w *Window) Reset() { w.next, w.size = 0, 0 }

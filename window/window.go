// Package window 是固定容量的滑动窗口（环形缓冲）。不依赖其他包。
package window

import "errors"

// ErrInvalidConfig 在容量非法时返回。
var ErrInvalidConfig = errors.New("window: capacity must be > 0")

// Window 保存最近写入的至多 cap 个字节。
type Window struct {
	buf  []byte
	pos  int // 下一个写入槽位
	size int // 已保存字节数（≤ cap）
}

// New 创建容量为 cap 的窗口。
func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrInvalidConfig
	}
	return &Window{buf: make([]byte, capacity)}, nil
}

// Cap 返回窗口容量。
func (w *Window) Cap() int { return len(w.buf) }

// Len 返回当前保存的字节数。
func (w *Window) Len() int { return w.size }

// Add 追加一个字节，超出容量时覆盖最旧字节。
func (w *Window) Add(b byte) {
	w.buf[w.pos] = b
	w.pos++
	if w.pos == len(w.buf) {
		w.pos = 0
	}
	if w.size < len(w.buf) {
		w.size++
	}
}

// At 按距离取回历史字节：dist=1 为最近写入的字节。
func (w *Window) At(dist int) byte {
	i := w.pos - dist
	if i < 0 {
		i += len(w.buf)
	}
	return w.buf[i]
}

// Reset 清空窗口。
func (w *Window) Reset() { w.pos, w.size = 0, 0 }

// Prefill 用 data 末尾至多 cap 个字节预置窗口（用于并行块预置字典）。
func (w *Window) Prefill(data []byte) {
	w.Reset()
	start := 0
	if len(data) > len(w.buf) {
		start = len(data) - len(w.buf)
	}
	for _, b := range data[start:] {
		w.Add(b)
	}
}

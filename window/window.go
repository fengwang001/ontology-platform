// Package window 提供固定容量的环形滑动窗口，支持按距离取回历史字节。
package window

import "errors"

// ErrCapacity 表示非法的窗口容量。
var ErrCapacity = errors.New("window: capacity must be positive")

// Window 是固定容量的环形缓冲，记录写入的全部字节数。
type Window struct {
	buf   []byte
	total int64
}

// New 创建容量为 capacity 的窗口；capacity <= 0 时返回错误。
func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrCapacity
	}
	return &Window{buf: make([]byte, capacity)}, nil
}

// Write 追加一个字节，最老的字节被覆盖。
func (w *Window) Write(b byte) {
	w.buf[w.total%int64(len(w.buf))] = b
	w.total++
}

// Len 返回写入过的字节总数。
func (w *Window) Len() int64 { return w.total }

// Cap 返回窗口容量。
func (w *Window) Cap() int64 { return int64(len(w.buf)) }

// At 返回距离末尾 dist 个字节的历史字节（1 <= dist <= min(Len, Cap)）。
// 越界时返回 0；调用方需先自行校验距离。
func (w *Window) At(dist int64) byte {
	p := w.total - dist
	if p < 0 || p < w.total-int64(len(w.buf)) {
		return 0
	}
	return w.buf[p%int64(len(w.buf))]
}

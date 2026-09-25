// Package window 实现固定容量的滑动窗口（环形缓冲），支持按距离取回历史字节。
// 不依赖其他包。
package window

import "errors"

// ErrBadCapacity 表示窗口容量非法（<=0）。
var ErrBadCapacity = errors.New("window: capacity must be positive")

// Window 是固定容量的环形字节缓冲，记住最近写入的 cap 个字节。
type Window struct {
	buf   []byte
	total int // 历史写入总字节数
}

// New 创建容量为 capacity 的窗口；capacity<=0 时返回 ErrBadCapacity。
func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrBadCapacity
	}
	return &Window{buf: make([]byte, capacity)}, nil
}

// Cap 返回窗口容量。
func (w *Window) Cap() int { return len(w.buf) }

// Total 返回历史写入总字节数。
func (w *Window) Total() int { return w.total }

// Add 追加一个字节，最旧的字节被覆盖。
func (w *Window) Add(b byte) {
	w.buf[w.total%len(w.buf)] = b
	w.total++
}

// Get 按距离取回历史字节：dist=1 是最近写入的字节。
// 调用方保证 1 <= dist <= min(total, cap)。
func (w *Window) Get(dist int) byte {
	return w.buf[(w.total-dist)%len(w.buf)]
}

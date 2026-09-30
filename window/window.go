// Package window 提供固定容量的环形滑动窗口，支持按距离取回历史字节。
// 不依赖其他包。单个实例不是并发安全的。
package window

import "errors"

// ErrZeroCapacity 在容量为 0 时返回。
var ErrZeroCapacity = errors.New("window: capacity must be positive")

// Window 是固定容量的环形缓冲，保存最近写入的 cap 个字节。
type Window struct {
	buf  []byte
	next int // 下一个写入位置
	len  int // 当前有效字节数（<= cap）
}

// New 创建容量为 cap 的窗口；cap 为 0 时拒绝。
func New(cap int) (*Window, error) {
	if cap <= 0 {
		return nil, ErrZeroCapacity
	}
	return &Window{buf: make([]byte, cap)}, nil
}

// Must 同 New，但非法配置直接 panic，供内部已校验的路径使用。
func Must(cap int) *Window {
	w, err := New(cap)
	if err != nil {
		panic(err)
	}
	return w
}

// Cap 返回窗口容量。
func (w *Window) Cap() int { return len(w.buf) }

// Len 返回当前有效字节数。
func (w *Window) Len() int { return w.len }

// Push 写入一个字节，覆盖最旧的历史。
func (w *Window) Push(b byte) {
	w.buf[w.next] = b
	w.next = (w.next + 1) % len(w.buf)
	if w.len < len(w.buf) {
		w.len++
	}
}

// At 按距离取回历史字节：dist=1 是最近写入的字节。
// dist 必须在 [1, Len()] 内，否则 panic（调用方需先校验）。
func (w *Window) At(dist int) byte {
	idx := (w.next - dist + len(w.buf)) % len(w.buf)
	return w.buf[idx]
}

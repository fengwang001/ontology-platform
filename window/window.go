// Package window 提供固定容量的滑动窗口（环形缓冲），
// 支持按绝对位置或按距离取回历史字节。本包不依赖其他包。
package window

import "errors"

// ErrBadCapacity 表示窗口容量非法（≤0）。
var ErrBadCapacity = errors.New("window: capacity must be positive")

// Window 是固定容量的环形字节缓冲，记住最近写入的 capacity 字节。
// 单个实例不要求并发安全。
type Window struct {
	buf   []byte
	next  int   // 下一个写入位置
	total int64 // 历史写入总字节数
}

// New 创建容量为 capacity 的窗口；capacity ≤ 0 时拒绝。
func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrBadCapacity
	}
	return &Window{buf: make([]byte, capacity)}, nil
}

// Capacity 返回窗口容量。
func (w *Window) Capacity() int { return len(w.buf) }

// Total 返回历史写入总字节数（可能大于容量）。
func (w *Window) Total() int64 { return w.total }

// Append 写入一个字节，必要时覆盖最老字节。
func (w *Window) Append(b byte) {
	w.buf[w.next] = b
	w.next = (w.next + 1) % len(w.buf)
	w.total++
}

// AppendBytes 连续写入一段字节。
func (w *Window) AppendBytes(p []byte) {
	for _, b := range p {
		w.Append(b)
	}
}

// Abs 读取绝对位置 pos 的字节；pos 必须仍在窗口内
// （total-capacity ≤ pos < total），否则行为未定义。
func (w *Window) Abs(pos int64) byte {
	return w.buf[pos%int64(len(w.buf))]
}

// At 按距离取回历史字节：dist=1 为最近写入的字节。
// dist 必须满足 1 ≤ dist ≤ min(total, capacity)。
func (w *Window) At(dist int64) byte {
	return w.Abs(w.total - dist)
}

// Package window 实现固定容量环形滑动窗口。
package window

// Window 是容量固定的环形字节缓冲。
type Window struct {
	buf []byte
	pos int
	n   int
}

// New 创建容量为 cap 的空窗口。
func New(capacity int) *Window {
	return &Window{buf: make([]byte, capacity)}
}

// Push 追加一个字节。
func (w *Window) Push(c byte) {}

// At 返回距离当前末尾 dist 处的字节，dist>=1。
func (w *Window) At(dist int) byte { return 0 }

// Len 返回窗口内有效字节数。
func (w *Window) Len() int { return w.n }

// Cap 返回窗口容量。
func (w *Window) Cap() int { return len(w.buf) }

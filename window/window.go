// Package window 是固定容量的环形字节历史窗口。
package window

import "errors"

// ErrBadConfig 在容量非法时返回。
var ErrBadConfig = errors.New("window: capacity must be positive")

// Window 保留最近至多 cap 个字节，distance 从 1 开始（1 表示最新字节）。
type Window struct {
	buf   []byte
	cap   int
	head  int // 下一个写入位置
	count int // 已存字节数（<= cap）
	total int // 累计写入总数（含被覆盖的）
}

// New 创建容量为 cap 的窗口。
func New(cap int) (*Window, error) {
	if cap <= 0 {
		return nil, ErrBadConfig
	}
	return &Window{buf: make([]byte, cap), cap: cap}, nil
}

// Cap 返回窗口容量。
func (w *Window) Cap() int { return w.cap }

// Len 返回当前保存的字节数。
func (w *Window) Len() int { return w.count }

// Total 返回累计写入的字节数。
func (w *Window) Total() int { return w.total }

// Add 追加一个字节，超出容量时覆盖最旧字节。
func (w *Window) Add(b byte) {
	w.buf[w.head] = b
	w.head++
	if w.head == w.cap {
		w.head = 0
	}
	if w.count < w.cap {
		w.count++
	}
	w.total++
}

// Write 批量追加字节，只保留最后 cap 个。
func (w *Window) Write(p []byte) {
	if len(p) >= w.cap {
		copy(w.buf, p[len(p)-w.cap:])
		w.head = 0
		w.count = w.cap
		w.total += len(p)
		return
	}
	for _, b := range p {
		w.Add(b)
	}
}

// At 返回距离 distance 的历史字节；distance 越界返回 false。
func (w *Window) At(distance int) (byte, bool) {
	if distance <= 0 || distance > w.count {
		return 0, false
	}
	idx := w.head - distance
	if idx < 0 {
		idx += w.cap
	}
	return w.buf[idx], true
}

// Suffix 返回最近 n 个字节的拷贝（n<=Len）。
func (w *Window) Suffix(n int) []byte {
	if n > w.count {
		n = w.count
	}
	out := make([]byte, n)
	for i := n; i > 0; i-- {
		b, _ := w.At(n - i + 1)
		out[i-1] = b
	}
	return out
}

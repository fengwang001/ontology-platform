package window

import "errors"

var ErrZeroCap = errors.New("window: capacity must be > 0")

// Window 是固定容量环形缓冲，保存最近 cap 个已输出字节。
type Window struct {
	buf []byte
	pos int
	n   int
}

func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrZeroCap
	}
	return &Window{buf: make([]byte, capacity)}, nil
}

func (w *Window) Cap() int { return len(w.buf) }
func (w *Window) Len() int { return w.n }

// Put 写入一个已输出字节（最旧的字节被覆盖）。
func (w *Window) Put(b byte) {
	w.buf[w.pos] = b
	w.pos++
	if w.pos == len(w.buf) {
		w.pos = 0
	}
	if w.n < len(w.buf) {
		w.n++
	}
}

// At 按 1-based 距离取回历史字节：At(1) 是最近写入的字节。
func (w *Window) At(distance int) (byte, bool) {
	if distance <= 0 || distance > w.n {
		return 0, false
	}
	i := w.pos - distance
	if i < 0 {
		i += len(w.buf)
	}
	return w.buf[i], true
}

// Seed 预置一段字典（按顺序写入，最旧部分按容量截断）。
func (w *Window) Seed(p []byte) {
	if len(p) > len(w.buf) {
		p = p[len(p)-len(w.buf):]
	}
	for _, b := range p {
		w.Put(b)
	}
}

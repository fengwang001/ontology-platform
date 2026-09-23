package window

import "errors"

// ErrConfig 在窗口容量非法时返回。
var ErrConfig = errors.New("window: capacity must be > 0")

// Window 是固定容量的环形字节缓冲，保留最近 cap 个字节。
type Window struct {
	buf []byte
	n   int64 // 累计写入（含预置字典）的绝对字节数
}

// New 创建容量为 cap 的窗口。
func New(cap int) (*Window, error) {
	if cap <= 0 {
		return nil, ErrConfig
	}
	return &Window{buf: make([]byte, 0, cap)}, nil
}

// Cap 返回窗口容量。
func (w *Window) Cap() int { return cap(w.buf) }

// Total 返回累计写入的绝对字节数。
func (w *Window) Total() int64 { return w.n }

// Len 返回当前保存的字节数（≤ Cap）。
func (w *Window) Len() int { return len(w.buf) }

// Append 写入若干字节，超出容量的旧字节被覆盖。
func (w *Window) Append(p []byte) {
	for _, b := range p {
		if len(w.buf) < cap(w.buf) {
			w.buf = append(w.buf, b)
		} else {
			w.buf[w.n%int64(cap(w.buf))] = b
		}
		w.n++
	}
}

// At 返回绝对位置 pos 的字节；越界或已滑出窗口返回 (0,false)。
func (w *Window) At(pos int64) (byte, bool) {
	if pos < 0 || pos >= w.n || w.n-pos > int64(len(w.buf)) {
		return 0, false
	}
	return w.buf[uint64(pos)%uint64(cap(w.buf))], true
}

// Back 返回「最近写入的第 distance 个」字节，distance 从 1 起；越界返回 (0,false)。
func (w *Window) Back(distance int) (byte, bool) {
	return w.At(w.n - int64(distance))
}

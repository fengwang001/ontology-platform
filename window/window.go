package window

import "errors"

var ErrBadConfig = errors.New("window: capacity must be > 0")

// Window 是固定容量环形字节缓冲，按“绝对位置”索引。
// Seed 可把一段历史作为预置字典装入（并行块用）。
type Window struct {
	buf   []byte
	cap   int
	start int // buf 中最旧字节的位置
	n     int // 已存字节数
	base  int // 最旧已存字节的绝对位置
}

func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrBadConfig
	}
	return &Window{buf: make([]byte, capacity), cap: capacity}, nil
}

// Seed 用 prev 的末尾至多 cap 个字节作为预置历史。
func (w *Window) Seed(prev []byte) {
	w.start, w.n, w.base = 0, 0, 0
	k := len(prev)
	if k > w.cap {
		prev = prev[k-w.cap:]
		k = w.cap
	}
	copy(w.buf, prev)
	w.n = k
	w.base = len(prev) - k
}

func (w *Window) Push(b byte) {
	if w.n < w.cap {
		w.buf[(w.start+w.n)%w.cap] = b
		w.n++
		return
	}
	w.buf[w.start] = b
	w.start = (w.start + 1) % w.cap
	w.base++
}

func (w *Window) Len() int { return w.n }

func (w *Window) Cap() int { return w.cap }

// LastPos 返回最近写入字节的绝对位置；空窗口返回 -1。
func (w *Window) LastPos() int { return w.base + w.n - 1 }

// AtAbs 取绝对位置 pos 的字节；pos 必须仍在窗口内。
func (w *Window) AtAbs(pos int) byte {
	off := pos - w.base
	return w.buf[(w.start+off)%w.cap]
}

// AtDist 按距离取字节：1 表示最近一个字节。
func (w *Window) AtDist(dist int) byte {
	return w.AtAbs(w.LastPos() - dist + 1)
}

func (w *Window) Contains(pos int) bool {
	return pos >= w.base && pos < w.base+w.n
}

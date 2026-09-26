// Package window 实现固定容量的滑动窗口（环形缓冲），支持按距离取回历史字节。
// 本包不依赖其他包。
package window

import "errors"

// ErrCapacity 表示非法的窗口容量（<= 0）。
var ErrCapacity = errors.New("window: capacity must be positive")

// Window 是固定容量的环形字节缓冲，记住最近写入的 Cap() 个字节。
type Window struct {
	buf     []byte
	start   int // 最老字节在 buf 中的下标
	n       int
	total   int64  // 历史追加总字节数
	scratch []byte // Repeat 复用的快照缓冲
}

// New 创建容量为 cap 的窗口；cap <= 0 时返回 ErrCapacity。
func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrCapacity
	}
	return &Window{buf: make([]byte, capacity)}, nil
}

// Len 返回当前持有的字节数（<= Cap）。
func (w *Window) Len() int { return w.n }

// Cap 返回窗口容量。
func (w *Window) Cap() int { return len(w.buf) }

// Total 返回历史追加总字节数。
func (w *Window) Total() int64 { return w.total }

// Append 追加一个字节，容量满时挤出最老字节。
func (w *Window) Append(b byte) {
	if w.n < len(w.buf) {
		w.buf[(w.start+w.n)%len(w.buf)] = b
		w.n++
	} else {
		w.buf[w.start] = b
		w.start = (w.start + 1) % len(w.buf)
	}
	w.total++
}

// AppendBytes 追加一段字节。
func (w *Window) AppendBytes(p []byte) {
	for _, b := range p {
		w.Append(b)
	}
}

// At 按距离取字节：dist=1 为最近写入的字节。dist 越界时 panic（内部约定）。
func (w *Window) At(dist int) byte {
	if dist < 1 || dist > w.n {
		panic("window: distance out of range")
	}
	return w.buf[(w.start+w.n-dist)%len(w.buf)]
}

// Abs 按绝对位置取字节，pos 必须落在 [Total-Len, Total) 内，否则 panic。
func (w *Window) Abs(pos int64) byte {
	off := pos - (w.total - int64(w.n))
	if off < 0 || off >= int64(w.n) {
		panic("window: absolute position out of range")
	}
	return w.buf[(w.start+int(off))%len(w.buf)]
}

// Repeat 按逐字节前向复制语义追加 length 个字节（第 i 字节取自当时末尾前
// dist 处），返回追加的内容。分块实现：每轮快照 n = min(dist, 剩余) 个源字节
// （n <= dist 保证源区间完整落在已写字节内）再整体追加，等价于逐字节复制。
func (w *Window) Repeat(dist, length int) []byte {
	if dist < 1 || dist > w.n {
		panic("window: repeat distance out of range")
	}
	out := make([]byte, 0, length)
	for rem := length; rem > 0; {
		n := min(dist, rem)
		if cap(w.scratch) < n {
			w.scratch = make([]byte, n)
		}
		w.scratch = w.scratch[:n]
		for i := range w.scratch {
			w.scratch[i] = w.buf[(w.start+w.n-dist+i)%len(w.buf)]
		}
		for _, b := range w.scratch {
			w.Append(b)
		}
		out = append(out, w.scratch...)
		rem -= n
	}
	return out
}

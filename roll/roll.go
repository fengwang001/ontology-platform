// Package roll 提供固定窗口的滚动字节哈希，推进一个字节为 O(1)。
package roll

import "errors"

// ErrWindow 表示窗口长度非法（窗口长度为 0 的可判定错误）。
var ErrWindow = errors.New("roll: window length must be >= 1")

const prime uint64 = 1099511628211

// Hasher 维护窗口 b[0..w-1] 的哈希
// h = b[0]*p^(w-1) + ... + b[w-1] (mod 2^64)。
type Hasher struct {
	w      int
	h      uint64
	lead   uint64 // p^(w-1)
	buf    []byte
	pos    int
	filled int
	// pushes 记录“进一个字节、出一个字节”的推进次数（非导出）。
	pushes int64
}

// New 创建窗口长度为 window 的滚动哈希。
func New(window int) (*Hasher, error) {
	if window < 1 {
		return nil, ErrWindow
	}
	lead := uint64(1)
	for i := 0; i < window-1; i++ {
		lead *= prime
	}
	return &Hasher{w: window, lead: lead, buf: make([]byte, window)}, nil
}

// Reset 清空窗口与推进计数，保留窗口配置。
func (h *Hasher) Reset() {
	h.h, h.pos, h.filled, h.pushes = 0, 0, 0, 0
	for i := range h.buf {
		h.buf[i] = 0
	}
}

// Push 送入一个字节。窗口填满后，每次调用执行一次“出新进旧”的 O(1) 推进。
func (h *Hasher) Push(b byte) {
	if h.filled < h.w {
		h.h = h.h*prime + uint64(b)
		h.buf[h.pos] = b
		h.pos = (h.pos + 1) % h.w
		h.filled++
		return
	}
	out := h.buf[h.pos]
	h.h = (h.h-uint64(out)*h.lead)*prime + uint64(b)
	h.buf[h.pos] = b
	h.pos = (h.pos + 1) % h.w
	h.pushes++
}

// Full 报告窗口是否已被填满（填满后 Push 才产生推进）。
func (h *Hasher) Full() bool { return h.filled == h.w }

// Hash 返回当前窗口哈希。
func (h *Hasher) Hash() uint64 { return h.h }

// Window 返回窗口长度。
func (h *Hasher) Window() int { return h.w }

// pushesCount 返回推进次数（包内可见，供同包测试断言）。
func (h *Hasher) pushesCount() int64 { return h.pushes }

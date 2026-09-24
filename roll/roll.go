// Package roll 提供固定窗口的滚动哈希（Rabin-Karp 风格）。
// 每喂入一个字节：窗口未满则填入，窗口已满则"进一个出一个"，O(1) 更新。
package roll

import "errors"

const (
	base   uint64 = 1099511628211
	modBit        = 6
	// Mask 与 Bits 暴露边界谓词 H&Mask==0 所需的模数信息（H mod 2^Bits == 0）。
	Mask uint64 = 1<<Bits - 1
	// Bits 为边界谓词取模的比特数，平均 2^Bits 个字节命中一次。
	Bits = modBit
)

// ErrZeroWindow 在窗口长度为 0 时返回。
var ErrZeroWindow = errors.New("roll: window length must be > 0")

// Hasher 是单线程使用的固定窗口滚动哈希。
type Hasher struct {
	win      []byte
	pos      int
	full     bool
	hash     uint64
	drop     uint64 // base^w
	advances uint64 // 非导出：记录"进一个出一个"的执行次数
}

// New 创建窗口长度为 w 的滚动哈希；w 必须大于 0。
func New(w int) (*Hasher, error) {
	if w <= 0 {
		return nil, ErrZeroWindow
	}
	h := &Hasher{win: make([]byte, w)}
	pow := uint64(1)
	for range w {
		pow *= base
	}
	h.drop = pow
	return h, nil
}

// Reset 清空窗口与全部计数（含推进次数）。
func (h *Hasher) Reset() {
	for i := range h.win {
		h.win[i] = 0
	}
	h.pos, h.full, h.hash, h.advances = 0, false, 0, 0
}

// Push 喂入一个字节并返回当前窗口哈希；窗口未满时返回建窗中的部分哈希。
func (h *Hasher) Push(b byte) uint64 {
	if h.full {
		out := h.win[h.pos]
		h.hash = h.hash*base + uint64(b) - uint64(out)*h.drop
		h.advances++
	} else {
		h.hash = h.hash*base + uint64(b)
	}
	h.win[h.pos] = b
	h.pos++
	if h.pos == len(h.win) {
		h.pos = 0
		if !h.full {
			h.full = true
		}
	}
	return h.hash
}

// Full 报告窗口是否已喂满（满窗哈希才可用于边界判定）。
func (h *Hasher) Full() bool { return h.full }

// Hash 返回当前窗口哈希。
func (h *Hasher) Hash() uint64 { return h.hash }

// Advances 返回"进一个出一个"已执行的次数（诊断/自检只读值）。
func (h *Hasher) Advances() uint64 { return h.advances }

// Clone 返回包含相同窗口与推进次数的副本；副本与原件此后互不影响。
func (h *Hasher) Clone() *Hasher {
	win := append([]byte(nil), h.win...)
	return &Hasher{win: win, pos: h.pos, full: h.full, hash: h.hash, drop: h.drop, advances: h.advances}
}

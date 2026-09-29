// Package roll 是宽度固定的字节窗口滚动哈希（Rabin-Karp 风格，模 2^64）。
//
// 窗口未满时哈希只覆盖已进入的字节；窗口满后，每 Push 一个字节执行恰好一次
// 「进一个、出一个」的 O(1) 增量更新。推进次数记录在非导出字段 advances 中。
package roll

import "errors"

// ErrBadWindow 在窗口长度为 0 时由 New 返回。
var ErrBadWindow = errors.New("roll: window length must be > 0")

const (
	base = 1099511628211 // FNV 素数，乘法在 uint64 上自然模 2^64
	bits = 13            // 谓词默认掩码低 bits 位
)

// Hasher 维护最近 width 个字节的滚动哈希。零值不可用，必须经 New 构造。
type Hasher struct {
	width    int
	buf      []byte
	pos      int
	filled   bool
	hash     uint64
	power    uint64 // base^(width-1)
	advances uint64 // 非导出：「进一个出一个」执行次数
}

// New 创建宽度 width 的滚动哈希。width 为 0 时返回 ErrBadWindow。
func New(width int) (*Hasher, error) {
	if width <= 0 {
		return nil, ErrBadWindow
	}
	power := uint64(1)
	for i := 0; i < width-1; i++ {
		power *= base
	}
	return &Hasher{width: width, buf: make([]byte, width), power: power}, nil
}

// Push 让 b 进入窗口；窗口满时同时退出最旧字节，并计一次推进。
func (h *Hasher) Push(b byte) {
	if h.filled {
		old := h.buf[h.pos]
		h.hash = (h.hash-uint64(old)*h.power)*base + uint64(b)
	} else {
		h.hash = h.hash*base + uint64(b)
		h.buf[h.pos] = b
		h.pos++
		if h.pos == h.width {
			h.pos = 0
			h.filled = true
		}
		return
	}
	h.buf[h.pos] = b
	h.pos++
	if h.pos == h.width {
		h.pos = 0
	}
	h.advances++
}

// Sum 返回当前窗口哈希；窗口未满时为前缀哈希。
func (h *Hasher) Sum() uint64 { return h.hash }

// Full 报告窗口是否已收满 width 个字节。
func (h *Hasher) Full() bool { return h.filled }

// AdvancesCount 仅供工程内其他包读取推进计数器用于自检/演示；它不属于滚动哈希
// 的配置或分块接口，字段本身始终是非导出的。
func (h *Hasher) AdvancesCount() uint64 { return h.advances }

// Width 返回窗口宽度。
func (h *Hasher) Width() int { return h.width }

// Mask 返回低 b 位全零谓词所用的掩码；b<=0 时使用默认位数。
func Mask(b int) uint64 {
	if b <= 0 {
		b = bits
	}
	return uint64(1)<<uint(b) - 1
}

// Boundary 是「哈希低 b 位全 0 且窗口已满」的内容边界谓词。
func Boundary(h *Hasher, b int) bool {
	return h.Full() && h.Sum()&Mask(b) == 0
}

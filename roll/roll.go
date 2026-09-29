// Package roll 提供定宽字节窗口的滚动哈希：进一个字节、出一个字节，O(1) 推进。
package roll

import "errors"

// ErrWindow 是窗口长度非法（0）的哨兵错误。
var ErrWindow = errors.New("roll: window length must be > 0")

const (
	base = uint32(31)
	mod  = uint32(1 << 24)
	mask = mod - 1
)

// Hasher 维护长度恰好为 window 的滑动窗口内容指纹。
type Hasher struct {
	window int
	ring   []byte
	pos    int
	filled int
	pow    uint32 // base^(window-1)
	h      uint32

	// advances 记录完整执行"进一个字节出一个字节"的次数（非导出）。
	advances int
	// pushed 记录曾经进入窗口的字节总数，用于断言没有字节被重复推进。
	pushed int
}

// New 创建窗口长度为 window 的滚动哈希。
func New(window int) (*Hasher, error) {
	if window <= 0 {
		return nil, ErrWindow
	}
	pow := uint32(1)
	for i := 0; i < window-1; i++ {
		pow = (pow * base) & mask
	}
	return &Hasher{window: window, ring: make([]byte, window), pow: pow}, nil
}

// Push 送入一个字节。窗口填满前只更新填充状态；填满后每调用一次即完成一次
// "进一个、出一个"的 O(1) 推进，advances 恰好加一。
func (x *Hasher) Push(b byte) {
	x.pushed++
	if x.filled < x.window {
		x.ring[x.pos] = b
		x.pos = (x.pos + 1) % x.window
		x.filled++
		x.h = (x.h*base + uint32(b)) & mask
		return
	}
	old := x.ring[x.pos]
	x.h = ((x.h-uint32(old)*x.pow)*base + uint32(b)) & mask
	x.ring[x.pos] = b
	x.pos = (x.pos + 1) % x.window
	x.advances++
}

// Full 报告窗口是否已填满（填满后的 Sum 才覆盖完整窗口）。
func (x *Hasher) Full() bool { return x.filled == x.window }

// Sum 返回当前窗口哈希。滚动维护的多项式值低字节混合较弱（结构化输入会集中），
// 出口处做一次与窗口内容无关的双字混合，谓词据此判定；O(1) 且不影响增量性质。
func (x *Hasher) Sum() uint32 {
	z := x.h + 0x9e3779b9
	z ^= z >> 16
	z *= 0x7feb352d
	z ^= z >> 15
	return z
}

// Reset 清空窗口并把推进计数归零（每开一个新块时调用）。
func (x *Hasher) Reset() {
	x.pos, x.filled, x.h, x.advances, x.pushed = 0, 0, 0, 0, 0
}

// Snapshot 返回包含环形缓冲区在内的深拷贝，用于失败后精确恢复哈希状态。
func (x *Hasher) Snapshot() Hasher {
	cp := *x
	cp.ring = append([]byte(nil), x.ring...)
	return cp
}

// Window 返回窗口长度。
func (x *Hasher) Window() int { return x.window }

// Advances 返回"进一个字节出一个字节"已执行的次数（只读统计；计数器字段
// advances 本身保持非导出，不出现在任何可设置的公开接口里）。
func (x *Hasher) Advances() int { return x.advances }

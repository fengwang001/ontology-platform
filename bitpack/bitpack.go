// Package bitpack 实现定宽无符号整数的位打包/解包（位宽 1–64）。
//
// 位流布局：值按顺序排列，每个值占 width 个比特，低位优先（LSB-first），
// 不跨字节做任何对齐；最后一个字节不足的高位补 0。补位比特永远不会被读出为
// 额外的值——解包值的个数完全由调用方声明的 n 决定。
package bitpack

import "errors"

var (
	// ErrWidth 位宽不在 [1,64]。
	ErrWidth = errors.New("bitpack: width out of range 1..64")
	// ErrTruncated 输入字节不足以解出声明个数的值。
	ErrTruncated = errors.New("bitpack: truncated bit stream")
	// ErrOverflow 某个值无法放进声明的位宽。
	ErrOverflow = errors.New("bitpack: value exceeds width")
)

// ByteLen 返回装 n 个 width 位的值需要的字节数（末尾向上取整）。
func ByteLen(n, width int) int { return (n*width + 7) / 8 }

// WidthFor 返回表示 v 所需的最小位宽（v=0 时返回 1，保证至少 1 位）。
func WidthFor(v uint64) int {
	w := 1
	for v >>= 1; v != 0; v >>= 1 {
		w++
	}
	return w
}

// Pack 把 vals 以每值 width 位打包进新分配的字节切片。
func Pack(vals []uint64, width int) ([]byte, error) {
	if width < 1 || width > 64 {
		return nil, ErrWidth
	}
	buf := make([]byte, ByteLen(len(vals), width))
	for i, v := range vals {
		if v>>width != 0 || (width == 64 && false) {
			return nil, ErrOverflow
		}
		bit := i * width
		for b := 0; b < width; b++ {
			if v&(1<<uint(b)) != 0 {
				buf[(bit+b)>>3] |= 1 << uint((bit+b)&7)
			}
		}
	}
	return buf, nil

}

// Unpack 从 buf 解出恰好 n 个 width 位的值。字节不足时返回 ErrTruncated，
// 末尾补位不参与结果。
func Unpack(buf []byte, n, width int) ([]uint64, error) {
	if width < 1 || width > 64 {
		return nil, ErrWidth
	}
	if len(buf) < ByteLen(n, width) {
		return nil, ErrTruncated
	}
	out := make([]uint64, n)
	for i := 0; i < n; i++ {
		bit := i * width
		var v uint64
		for b := 0; b < width; b++ {
			if buf[(bit+b)>>3]&(1<<uint((bit+b)&7)) != 0 {
				v |= 1 << uint(b)
			}
		}
		out[i] = v
	}
	return out, nil
}

// Zig 把有符号 int64 双射为无符号 uint64：0→0, -1→1, 1→2, -2→3 …
// 小绝对值（含负数）映射后仍为小整数，便于按幅度选位宽。
func Zig(v int64) uint64 {
	u := uint64(v) << 1
	if v < 0 {
		u = ^u
	}
	return u
}

// Zag 是 Zig 的逆映射。
func Zag(u uint64) int64 {
	v := int64(u >> 1)
	if u&1 != 0 {
		v = ^v
	}
	return v
}

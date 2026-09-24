// Package bitpack 实现定宽无符号整数的位打包与解包。
//
// 布局为小端位序：第 i 个值占据从全局位偏移 i*width 开始的 width 个
// 连续比特，比特流按字节小端排列。宽度 1..64。末尾不足一字节时剩余比特
// 补零，解包严格按值个数读取，不会读出补位产生的多余值。
package bitpack

import "errors"

// ErrWidth 位宽越界（允许 1..64）。
var ErrWidth = errors.New("bitpack: width out of range 1..64")

// PackedSize 返回 n 个 width 位值占用的字节数。
func PackedSize(n, width int) int {
	return (n*width + 7) / 8
}

// WidthFor 返回表示 [0,max] 所需的最小位宽；max 必须非负。
// 约定 max==0 时宽度为 1（仍需一个比特表示唯一码字 0）。
func WidthFor(max uint64) int {
	w := 1
	for v := max >> 1; v != 0; v >>= 1 {
		w++
	}
	return w
}

// Pack 把 vals 紧密打包；超过 width 位的高位被忽略（调用方须保证值域）。
func Pack(vals []uint64, width int) []byte {
	if width < 1 || width > 64 {
		panic(ErrWidth)
	}
	buf := make([]byte, PackedSize(len(vals), width))
	writeVals(buf, vals, width)
	return buf
}

// Unpack 从 data 解出恰好 n 个值。data 长度不足或 width 非法时返回错误，
// 绝不越界读取。
func Unpack(data []byte, width, n int) ([]uint64, error) {
	if width < 1 || width > 64 {
		return nil, ErrWidth
	}
	if n < 0 || len(data) < PackedSize(n, width) {
		return nil, errors.New("bitpack: short buffer")
	}
	out := make([]uint64, n)
	for i := 0; i < n; i++ {
		var v uint64
		base := i * width
		for j := 0; j < width; j++ {
			p := base + j
			if data[p>>3]&(1<<uint(p&7)) != 0 {
				v |= 1 << uint(j)
			}
		}
		out[i] = v
	}
	return out, nil
}

// writeVals 逐位写入；紧密布局天然处理任意跨字节/跨字边界，末尾补零。
func writeVals(buf []byte, vals []uint64, width int) {
	for i, x := range vals {
		base := i * width
		for j := 0; j < width; j++ {
			if x&(1<<uint(j)) != 0 {
				p := base + j
				buf[p>>3] |= 1 << uint(p&7)
			}
		}
	}
}

// Package bitpack 实现定宽无符号整数的位打包/解包与有符号整数的 ZigZag 变换。
package bitpack

import "errors"

var (
	ErrBadWidth = errors.New("bitpack: width out of range 1..64")
	ErrShortBuf = errors.New("bitpack: buffer too short")
)

// ZigEncode 将有符号 int64 双射映射到非负 uint64，保持大小关系可逆。
func ZigEncode(v int64) uint64 {
	return uint64(v<<1) ^ uint64(v>>63)
}

// ZigDecode 是 ZigEncode 的逆映射。
func ZigDecode(u uint64) int64 {
	return int64(u>>1) ^ -int64(u&1)
}

// WidthFor 返回能容纳 max 的最小位宽（至少 1 位）。
func WidthFor(max uint64) uint8 {
	for w := uint8(1); w < 64; w++ {
		if max < 1<<w {
			return w
		}
	}
	return 64
}

// PackedLen 返回 n 个 width 位的值需要的字节数（向上取整到字节）。
func PackedLen(n int, width uint8) int {
	if n <= 0 {
		return 0
	}
	return int((int64(n)*int64(width) + 7) / 8)
}

// Pack 以 LSB-first 方式把 vals 按 width 位等宽打包，返回恰好 PackedLen 字节。
// 值超过 width 能表示的范围或 width 非法时返回错误。
func Pack(vals []uint64, width uint8) ([]byte, error) {
	if width < 1 || width > 64 {
		return nil, ErrBadWidth
	}
	out := make([]byte, PackedLen(len(vals), width))
	var bitPos uint64
	mask := uint64(1)<<width - 1
	if width == 64 {
		mask = ^uint64(0)
	}
	for _, v := range vals {
		if v&^mask != 0 {
			return nil, ErrBadWidth
		}
		off := bitPos / 8
		shift := bitPos % 8
		out[off] |= byte(v << shift)
		need := shift + uint64(width)
		for k := uint64(1); need > 8*k; k++ {
			out[off+k] = byte(v >> (8*k - shift))
		}
		bitPos += uint64(width)
	}
	return out, nil
}

// Unpack 是 Pack 的逆：从 data 读出 n 个 width 位的值。末尾不足一字节的补位按 0
// 处理，且只读取 n 个值，绝不返回多余值。
func Unpack(data []byte, n int, width uint8) ([]uint64, error) {
	if width < 1 || width > 64 {
		return nil, ErrBadWidth
	}
	if n < 0 || len(data) < PackedLen(n, width) {
		return nil, ErrShortBuf
	}
	out := make([]uint64, n)
	var bitPos uint64
	for i := 0; i < n; i++ {
		off := bitPos / 8
		shift := bitPos % 8
		var v uint64
		v = uint64(data[off]) >> shift
		need := shift + uint64(width)
		for k := uint64(1); need > 8*k; k++ {
			v |= uint64(data[off+k]) << (8*k - shift)
		}
		if width < 64 {
			v &= 1<<width - 1
		}
		out[i] = v
		bitPos += uint64(width)
	}
	return out, nil
}

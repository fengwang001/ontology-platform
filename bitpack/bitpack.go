// Package bitpack 提供定宽无符号整数的位打包与解包。
//
// 位宽 w 取值 1..64。值按小端位序依次写入位流：第 i 个值占据
// 位区间 [i*w, (i+1)*w)，低位在前。末尾不足一个完整字节的部分补零。
// 打包/解包只处理 n 个值，补位永远不会被读成多余的值。
package bitpack

import "errors"

// ErrInvalidWidth 表示位宽超出 1..64 范围。
var ErrInvalidWidth = errors.New("bitpack: width must be in [1,64]")

// ErrShortBuffer 表示打包数据长度不足，无法解出请求个数的值。
var ErrShortBuffer = errors.New("bitpack: buffer too short")

// PackedLen 返回打包 n 个宽度为 w 的值所需的字节数。
func PackedLen(w, n int) int {
	if w <= 0 || n <= 0 {
		return 0
	}
	return (n*w + 7) / 8
}

// Encode 把 vals 中的每个值按低 w 位打包，返回紧凑字节切片。
// 每个值只取低 w 位，高位被忽略。
func Encode(vals []uint64, w int) ([]byte, error) {
	if w < 1 || w > 64 {
		return nil, ErrInvalidWidth
	}
	out := make([]byte, PackedLen(w, len(vals)))
	if err := EncodeInto(out, vals, w); err != nil {
		return nil, err
	}
	return out, nil
}

// EncodeInto 与 Encode 相同，但写入调用方提供的缓冲区。
// dst 长度必须至少为 PackedLen(w, len(vals))。
func EncodeInto(dst []byte, vals []uint64, w int) error {
	if w < 1 || w > 64 {
		return ErrInvalidWidth
	}
	if len(dst) < PackedLen(w, len(vals)) {
		return ErrShortBuffer
	}
	// 128 位累积器：lo 为低位字，hi 为溢出的高位字。
	// 不变式：nbits < 8 时进入下一轮，故 nbits+w < 72 < 128，不会丢位。
	var lo, hi uint64
	var nbits int
	pos := 0
	mask := maskOf(w)
	for _, v := range vals {
		v &= mask
		lo |= v << nbits
		hi |= v >> (64 - nbits) // nbits=0 时右移 64 位得 0，正确
		nbits += w
		for nbits >= 8 {
			dst[pos] = byte(lo)
			lo = lo>>8 | hi<<56
			hi >>= 8
			nbits -= 8
			pos++
		}
	}
	if nbits > 0 {
		dst[pos] = byte(lo) // 末尾不足一字节，高位补零
	}
	return nil
}

// Decode 从 data 解出 n 个宽度为 w 的值，写入 dst（长度须 >= n）。
// data 长度不足时返回 ErrShortBuffer，此时 dst 内容未定义。
func Decode(dst []uint64, data []byte, w, n int) error {
	if w < 1 || w > 64 {
		return ErrInvalidWidth
	}
	if len(data) < PackedLen(w, n) {
		return ErrShortBuffer
	}
	mask := maskOf(w)
	var lo, hi uint64
	var nbits int
	pos := 0
	for i := 0; i < n; i++ {
		for nbits < w {
			b := uint64(data[pos])
			lo |= b << nbits
			hi |= b >> (64 - nbits)
			nbits += 8
			pos++
		}
		dst[i] = lo & mask // 值的 w 位全部落在 lo 内（w<=64 且起始位为 0）
		lo = lo>>w | hi<<(64-w)
		hi >>= w
		nbits -= w
	}
	return nil
}

// DecodeAll 是 Decode 的便捷封装，返回新分配的切片。
func DecodeAll(data []byte, w, n int) ([]uint64, error) {
	out := make([]uint64, n)
	if err := Decode(out, data, w, n); err != nil {
		return nil, err
	}
	return out, nil
}

// WidthFor 返回能表示 max 所需的最小位宽（1..64）。
func WidthFor(max uint64) int {
	w := 64 - clz64(max)
	if w < 1 {
		w = 1
	}
	return w
}

func maskOf(w int) uint64 {
	if w >= 64 {
		return ^uint64(0)
	}
	return (uint64(1) << w) - 1
}

func clz64(v uint64) int {
	if v == 0 {
		return 64
	}
	n := 0
	for v&0x8000000000000000 == 0 {
		v <<= 1
		n++
	}
	return n
}

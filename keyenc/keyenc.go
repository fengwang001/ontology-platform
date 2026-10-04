// Package keyenc 提供旧（E1）/ 新（E2）两种键编码以及逻辑区间到物理字节区间的映射。
//
// E1(k) 为 k 的 64 位补码大端 8 字节；E2(k) 为 E1(k) 的最高位取反。
// E2 下逻辑序与字节序一致；E1 下负数的字节序排在非负数之后。
package keyenc

import "encoding/binary"

// E1 返回旧编码：k 的 64 位补码大端 8 字节。
func E1(k int64) [8]byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(k))
	return b
}

// E2 返回新编码：E1(k) 的最高位取反，逻辑序与字节序一致。
func E2(k int64) [8]byte {
	b := E1(k)
	b[0] ^= 0x80
	return b
}

// Decode1 解码 E1 编码的键。
func Decode1(b []byte) int64 {
	return int64(binary.BigEndian.Uint64(b))
}

// Decode2 解码 E2 编码的键。
func Decode2(b []byte) int64 {
	return int64(binary.BigEndian.Uint64(b) ^ (1 << 63))
}

// Interval 为物理字节区间 [Lo, Hi)；Hi 为 nil 表示正无穷，Lo 为 nil 表示负无穷。
type Interval struct {
	Lo []byte
	Hi []byte
}

// Range1 把逻辑区间 [lo, hi) 映射为 E1 物理区间，负数段在前、非负段在后。
// 负数段上界 min(hi,0) 为 0 时物理上界是无穷（E1(0) 物理上排在所有负数之前）。
// hi 可以取 MaxKey+1（上界开区间编码仍合法）。
func Range1(lo, hi int64) []Interval {
	if lo >= hi {
		return nil
	}
	var out []Interval
	if lo < 0 {
		up := min(hi, 0)
		loB := E1(lo)
		iv := Interval{Lo: loB[:]}
		if up < 0 {
			hiB := E1(up)
			iv.Hi = hiB[:]
		}
		out = append(out, iv)
	}
	if hi > 0 {
		a := max(lo, 0)
		loB, hiB := E1(a), E1(hi)
		out = append(out, Interval{Lo: loB[:], Hi: hiB[:]})
	}
	return out
}

// Range2 把逻辑区间 [lo, hi) 映射为 E2 物理区间（保序，恒为单区间）。
func Range2(lo, hi int64) []Interval {
	if lo >= hi {
		return nil
	}
	loB, hiB := E2(lo), E2(hi)
	return []Interval{{Lo: loB[:], Hi: hiB[:]}}
}

// Package chunk 把记录集合切成定长块，并提供弱（滚动）与强两级校验和。
package chunk

import (
	"crypto/sha256"
	"sync/atomic"
)

// 非导出计数器：弱校验和基本运算次数、强校验和计算次数。
var (
	weakOps   atomic.Int64
	strongOps atomic.Int64
)

// WeakOps 返回弱校验和基本运算（加/减）的累计次数。
func WeakOps() int64 { return weakOps.Load() }

// StrongOps 返回强校验和的累计计算次数。
func StrongOps() int64 { return strongOps.Load() }

// ResetCounters 清零两个计数器。
func ResetCounters() {
	weakOps.Store(0)
	strongOps.Store(0)
}

// WeakSum 是加法型弱校验和：W(B) = Σ B[k] (mod 2^32)。
// 它可 O(1) 滚动，但极易碰撞，必须与 StrongSum 配合使用。
func WeakSum(b []byte) uint32 {
	var s uint32
	for _, v := range b {
		s += uint32(v)
		weakOps.Add(1)
	}
	return s
}

// Roll 把窗口从偏移 i 推进到 i+1：W(i+1) = W(i) - out + in，
// 其中 out 是移出窗口的字节，in 是移入窗口的字节。
func Roll(w uint32, out, in byte) uint32 {
	weakOps.Add(2)
	return w - uint32(out) + uint32(in)
}

// StrongSum 是强校验和（SHA-256），只在弱匹配时调用以确认内容。
func StrongSum(b []byte) [32]byte {
	strongOps.Add(1)
	return sha256.Sum256(b)
}

// NumBlocks 返回长度为 n 的数据按 size 切分后的块数；size 非正时返回 0。
func NumBlocks(n, size int) int {
	if size <= 0 || n <= 0 {
		return 0
	}
	return (n + size - 1) / size
}

// Block 返回第 i 块（末块可能不满）。调用方保证 0 <= i < NumBlocks。
func Block(data []byte, size, i int) []byte {
	lo := i * size
	hi := lo + size
	if hi > len(data) {
		hi = len(data)
	}
	return data[lo:hi]
}

// BlockLen 返回长度为 total 的数据按 size 切分后第 i 块的长度。
func BlockLen(total, size, i int) int {
	rest := total - i*size
	if rest < size {
		return rest
	}
	return size
}

// Package chunk 把记录集合切成定长块，并提供可滚动的弱校验和与
// 用于确认的强校验和。弱校验和只负责快速筛选（会碰撞），强校验和
// （SHA-256 截断）负责最终确认。
package chunk

import (
	"crypto/sha256"
	"errors"
	"sync/atomic"
)

// ErrInvalidBlockSize 表示块大小不大于零，属可判定错误。
var ErrInvalidBlockSize = errors.New("chunk: block size must be positive")

const mod = 1 << 16

// weakOps 记录弱校验和的基本运算次数：从头计算记 len(block)，
// 滚动一步记 2。用于断言全程代价不超过 4 × 数据长度。
var weakOps atomic.Uint64

// WeakOps 返回弱校验和基本运算的累计次数。
func WeakOps() uint64 { return weakOps.Load() }

// ResetWeakOps 清零弱校验和运算计数器。
func ResetWeakOps() { weakOps.Store(0) }

// Weak 是 32 位滚动弱校验和：低 16 位为字节和 a，高 16 位为位置加权和 b。
type Weak struct {
	a uint32
	b uint32
}

// SumWeak 从头计算一块数据的弱校验和，代价 O(len(block))。
func SumWeak(block []byte) Weak {
	weakOps.Add(uint64(len(block)))
	var a, b uint32
	for i, x := range block {
		a = (a + uint32(x)) % mod
		b = (b + uint32(len(block)-i)*uint32(x)) % mod
	}
	return Weak{a: a, b: b}
}

// Roll 把窗口从偏移 i 推进到 i+1：移出 out、移入 in，n 为窗口长度。
// 公式：a' = a - out + in；b' = b - n·out + a'。代价 O(1)。
func (w Weak) Roll(out, in byte, n int) Weak {
	weakOps.Add(2)
	a := (w.a - uint32(out) + uint32(in)) % mod
	b := (w.b - (uint32(n)*uint32(out))%mod + a) % mod
	return Weak{a: a, b: b}
}

// Value 返回打包后的 32 位弱校验和。
func (w Weak) Value() uint32 { return w.a | w.b<<16 }

// Strong 是 128 位强校验和（SHA-256 的前 16 字节），只在弱命中时计算。
type Strong [16]byte

// SumStrong 计算一块数据的强校验和。
func SumStrong(block []byte) Strong {
	sum := sha256.Sum256(block)
	var s Strong
	copy(s[:], sum[:16])
	return s
}

// NumBlocks 返回 dataLen 字节按 size 切分得到的块数（末块可不满）。
func NumBlocks(dataLen, size int) (int, error) {
	if size <= 0 {
		return 0, ErrInvalidBlockSize
	}
	return (dataLen + size - 1) / size, nil
}

// Block 返回 data 的第 i 块（末块可能不足 size）。i 越界时返回 nil。
func Block(data []byte, size, i int) []byte {
	if size <= 0 || i < 0 {
		return nil
	}
	start := i * size
	if start >= len(data) {
		return nil
	}
	end := start + size
	if end > len(data) {
		end = len(data)
	}
	return data[start:end]
}

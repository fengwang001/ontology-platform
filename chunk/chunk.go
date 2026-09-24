// Package chunk 把记录集合切成定长块，并提供弱（滚动）与强校验和。
package chunk

import (
	"crypto/sha256"
	"errors"
	"sync/atomic"
)

// ErrBlockSize 表示块大小非法（<= 0）。
var ErrBlockSize = errors.New("chunk: block size must be positive")

// Block 是一个定长块及其两级校验和；末块可能不足 blockSize。
type Block struct {
	Index  int
	Offset int
	Data   []byte
	Weak   uint32
	Strong [16]byte
}

// weakOps 是非导出计数器，记录弱校验和的基本运算（单字节加/减）次数。
var weakOps atomic.Uint64

// WeakOps 返回弱校验和基本运算的累计次数。
func WeakOps() uint64 { return weakOps.Load() }

// ResetWeakOps 清零弱校验和运算计数器。
func ResetWeakOps() { weakOps.Store(0) }

// WeakSum 计算加法型弱校验和，每字节记 1 次基本运算。
func WeakSum(b []byte) uint32 {
	var sum uint32
	for _, c := range b {
		sum += uint32(c)
	}
	weakOps.Add(uint64(len(b)))
	return sum
}

// Strong 计算强校验和（SHA-256 的前 16 字节）。
func Strong(b []byte) [16]byte {
	sum := sha256.Sum256(b)
	var out [16]byte
	copy(out[:], sum[:16])
	return out
}

// Roller 是弱校验和的滚动窗口：S(i+1) = S(i) - data[i] + data[i+bs]。
type Roller struct {
	sum uint32
}

// NewRoller 以首个窗口初始化滚动器。
func NewRoller(window []byte) *Roller {
	return &Roller{sum: WeakSum(window)}
}

// Sum 返回当前窗口的弱校验和。
func (r *Roller) Sum() uint32 { return r.sum }

// Roll 推进一个偏移：移出 out，移入 in，记 2 次基本运算。
func (r *Roller) Roll(out, in byte) uint32 {
	r.sum = r.sum - uint32(out) + uint32(in)
	weakOps.Add(2)
	return r.sum
}

// Split 把 data 切成定长块并计算两级校验和；空数据返回零个块。
func Split(data []byte, size int) ([]Block, error) {
	if size <= 0 {
		return nil, ErrBlockSize
	}
	var blocks []Block
	for off := 0; off < len(data); off += size {
		end := off + size
		if end > len(data) {
			end = len(data)
		}
		b := data[off:end]
		blocks = append(blocks, Block{
			Index:  len(blocks),
			Offset: off,
			Data:   b,
			Weak:   WeakSum(b),
			Strong: Strong(b),
		})
	}
	return blocks, nil
}

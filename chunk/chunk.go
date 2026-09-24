// Package chunk 把数据切成定长块，并提供可滚动的弱校验和与强校验和。
package chunk

import (
	"crypto/sha256"
	"errors"
	"sync/atomic"
)

// ErrBlockSize 表示块大小非法（<= 0），是可判定错误。
var ErrBlockSize = errors.New("chunk: block size must be positive")

var (
	weakOps   atomic.Int64 // 弱校验和基本运算次数
	strongOps atomic.Int64 // 强校验和计算次数
)

// Weak 返回数据的弱校验和（32 位加法和），计入基本运算次数。
func Weak(data []byte) uint32 {
	var sum uint32
	for _, b := range data {
		sum += uint32(b)
	}
	weakOps.Add(int64(len(data)))
	return sum
}

// RollSum 把弱校验和从偏移 i 推进到 i+1。
func RollSum(w uint32, out, in byte) uint32 {
	weakOps.Add(2)
	return w - uint32(out) + uint32(in)
}

// Strong 返回数据的强校验和（SHA-256），计入强校验次数。
func Strong(data []byte) [32]byte {
	strongOps.Add(1)
	return sha256.Sum256(data)
}

// Split 把 data 切成 bs 字节的定长块；末块不满时原样保留为最后一块。
func Split(data []byte, bs int) ([][]byte, error) {
	if bs <= 0 {
		return nil, ErrBlockSize
	}
	var blocks [][]byte
	for len(data) > 0 {
		n := bs
		if n > len(data) {
			n = len(data)
		}
		blocks = append(blocks, data[:n])
		data = data[n:]
	}
	return blocks, nil
}

// FullBlocks 返回 data 中完整块的数量（末块不满不计入）。
func FullBlocks(n, bs int) int {
	if bs <= 0 || n < bs {
		return 0
	}
	return n / bs
}

// Ops 返回弱校验和基本运算累计次数。
func Ops() int64 { return weakOps.Load() }

// Strongs 返回强校验和累计计算次数。
func Strongs() int64 { return strongOps.Load() }

// ResetCounters 清零两个计数器（测试与演示用）。
func ResetCounters() {
	weakOps.Store(0)
	strongOps.Store(0)
}

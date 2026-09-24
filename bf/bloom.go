// Package bf 是单个布隆过滤器：m 位、k=2 个哈希，位集存于进程内存。
package bf

import (
	"errors"
	"math/bits"
)

// K 是哈希函数个数，规则固定为 2。
const K = 2

// ErrInvalidM 表示位数组长度 m 不是正整数，可由 errors.Is 判定。
var ErrInvalidM = errors.New("bf: m must be a positive integer")

// Bloom 是一个定长位数组的布隆过滤器，零值不可用，请用 New 构造。
type Bloom struct {
	bits []uint64
	m    int
	n    int // 已写入键数（Add 调用次数）
}

// New 创建 m 位的布隆过滤器；m 非正时返回 ErrInvalidM。
func New(m int) (*Bloom, error) {
	if m <= 0 {
		return nil, ErrInvalidM
	}
	return &Bloom{bits: make([]uint64, (m+63)/64), m: m}, nil
}

// positions 按规则计算两个哈希位置：
// h1 = x mod m，h2 = (x*5+3) mod m，x 为各字节值之和。
func positions(key string, m int) (int, int) {
	var x int
	for i := 0; i < len(key); i++ {
		x += int(key[i])
	}
	return x % m, (x*5 + 3) % m
}

// Add 置位 key 的两个哈希位置并累加键数。
func (b *Bloom) Add(key string) {
	h1, h2 := positions(key, b.m)
	b.bits[h1>>6] |= 1 << uint(h1&63)
	b.bits[h2>>6] |= 1 << uint(h2&63)
	b.n++
}

// Contains 当且仅当两个哈希位都已置位时返回真。
func (b *Bloom) Contains(key string) bool {
	h1, h2 := positions(key, b.m)
	return b.bits[h1>>6]&(1<<uint(h1&63)) != 0 &&
		b.bits[h2>>6]&(1<<uint(h2&63)) != 0
}

// Merge 把 src 的全部置位单调 OR 进自身；src 必须与自身等长（同 m 构造）。
// 只并位，不并键数：封存层的键数由上层单独累计。
func (b *Bloom) Merge(src *Bloom) {
	for i := range b.bits {
		b.bits[i] |= src.bits[i]
	}
}

// Reset 清空全部位并把键数归零。
func (b *Bloom) Reset() {
	clear(b.bits)
	b.n = 0
}

// Count 返回已写入的键数。
func (b *Bloom) Count() int { return b.n }

// FillRatio 返回已置位位数占总长度 m 的比例，取值 [0,1]。
func (b *Bloom) FillRatio() float64 {
	var set int
	for _, word := range b.bits {
		set += bits.OnesCount64(word)
	}
	return float64(set) / float64(b.m)
}

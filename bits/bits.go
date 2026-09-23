// Package bits 是位图本体：置位、清位、查位，以及在范围内寻找连续空闲游程。
// 它不依赖本工程的其他任何包。
package bits

import "errors"

// ErrBadSize 在长度为 0 或负数时由 New 返回。
var ErrBadSize = errors.New("bits: non-positive bitmap size")

// Bitmap 是定长位图：0 空闲，1 占用。
type Bitmap struct {
	n   int
	one []uint64
}

// New 创建长度为 n 的全空闲位图。
func New(n int) (*Bitmap, error) {
	if n <= 0 {
		return nil, ErrBadSize
	}
	return &Bitmap{n: n, one: make([]uint64, (n+63)/64)}, nil
}

// Len 返回槽位总数。
func (b *Bitmap) Len() int { return b.n }

// IsSet 查询第 i 位是否占用；越界返回 false。
func (b *Bitmap) IsSet(i int) bool {
	if uint(i) >= uint(b.n) {
		return false
	}
	return b.one[i>>6]&(uint64(1)<<(i&63)) != 0
}

// Set 将第 i 位置为 1，越界返回错误且不改任何位。
func (b *Bitmap) Set(i int) error {
	if uint(i) >= uint(b.n) {
		return ErrOutOfRange
	}
	b.one[i>>6] |= uint64(1) << (i & 63)
	return nil
}

// Clear 将第 i 位清为 0，越界返回错误且不改任何位。
func (b *Bitmap) Clear(i int) error {
	if uint(i) >= uint(b.n) {
		return ErrOutOfRange
	}
	b.one[i>>6] &^= uint64(1) << (i & 63)
	return nil
}

// Ones 返回位图中 1（占用位）的总数。
func (b *Bitmap) Ones() int {
	c := 0
	for _, w := range b.one {
		for w != 0 {
			w &= w - 1
			c++
		}
	}
	return c
}

// FindFreeRun 返回 [lo,hi) 内首个长度至少为 k 的连续 0 游程的起点；不存在返回 -1。
func (b *Bitmap) FindFreeRun(lo, hi, k int) int {
	if k <= 0 || lo < 0 || hi > b.n || lo > hi {
		return -1
	}
	run := 0
	for i := lo; i < hi; i++ {
		if b.IsSet(i) {
			run = 0
			continue
		}
		run++
		if run >= k {
			return i - k + 1
		}
	}
	return -1
}

// ErrOutOfRange 表示下标越界。
var ErrOutOfRange = errors.New("bits: index out of range")

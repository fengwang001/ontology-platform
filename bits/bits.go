// Package bits 提供按索引读写的定长位数组。
package bits

import "errors"

// ErrOutOfRange 表示索引超出位数组长度。
var ErrOutOfRange = errors.New("bits: index out of range")

// BitSet 是定长位数组，仅并发读（Get）安全。
type BitSet struct {
	words []uint64
	n     uint64
}

// New 返回长度为 n 的位数组，全部位初始为 0。
func New(n uint64) *BitSet {
	return &BitSet{words: make([]uint64, (n+63)/64), n: n}
}

// Set 将第 i 位置 1；i 越界时返回 ErrOutOfRange。
func (b *BitSet) Set(i uint64) error {
	if i >= b.n {
		return ErrOutOfRange
	}
	b.words[i/64] |= 1 << (i % 64)
	return nil
}

// Get 返回第 i 位是否为 1；i 越界时返回 ErrOutOfRange。
func (b *BitSet) Get(i uint64) (bool, error) {
	if i >= b.n {
		return false, ErrOutOfRange
	}
	return b.words[i/64]&(1<<(i%64)) != 0, nil
}

// Len 返回位数组长度（位数）。
func (b *BitSet) Len() uint64 { return b.n }

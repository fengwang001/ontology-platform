// Package bits 提供按索引读写的位数组。
package bits

// Array 是定长位数组，按 uint64 字存储。
type Array struct {
	words []uint64
	n     uint64
}

// New 返回可存放 n 位的位数组，全部位初始为 0。
func New(n uint64) *Array {
	return &Array{words: make([]uint64, (n+63)/64), n: n}
}

// Set 将索引 i 处的位置 1，i 必须小于 Len()。
func (a *Array) Set(i uint64) {
	a.words[i/64] |= 1 << (i % 64)
}

// Get 返回索引 i 处的位，i 必须小于 Len()。
func (a *Array) Get(i uint64) bool {
	return a.words[i/64]&(1<<(i%64)) != 0
}

// Len 返回位数组的位数。
func (a *Array) Len() uint64 {
	return a.n
}

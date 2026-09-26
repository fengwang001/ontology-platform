// Package fnv 实现题目给定的哈希原语：32 位 FNV-1a、叶子哈希与节点合并。
package fnv

import "encoding/binary"

const (
	offset32 = 0x811C9DC5
	prime32  = 0x01000193
)

// Hash 即 fnv32：h 初值 0x811C9DC5，逐字节 h = (h ^ b) * 0x01000193（uint32 自然回绕）。
func Hash(b []byte) uint32 {
	h := uint32(offset32)
	for _, x := range b {
		h = (h ^ uint32(x)) * prime32
	}
	return h
}

// Leaf 即 leafHash(data) = fnv32(0x00 ‖ data)。
func Leaf(data []byte) uint32 {
	buf := make([]byte, 0, len(data)+1)
	buf = append(buf, 0x00)
	buf = append(buf, data...)
	return Hash(buf)
}

// Combine 即 combine(l, r) = fnv32(0x01 ‖ l ‖ r)，l、r 各按 4 字节大端编码。
func Combine(l, r uint32) uint32 {
	var buf [9]byte
	buf[0] = 0x01
	binary.BigEndian.PutUint32(buf[1:5], l)
	binary.BigEndian.PutUint32(buf[5:9], r)
	return Hash(buf[:])
}

// Package hsh 提供确定性哈希（FNV-1a + splitmix64 终混）以及桶下标与秩的抽取。
package hsh

import "math/bits"

// Hash 返回字符串 UTF-8 字节的确定性 64 位哈希。
func Hash(s string) uint64 {
	h := uint64(14695981039346656037)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	h ^= h >> 30
	h *= 0xbf58476d1ce4e5b9
	h ^= h >> 27
	h *= 0x94d049bb133111eb
	h ^= h >> 31
	return h
}

// Bucket 取哈希低 p 位作为桶下标 j = h & (2^p - 1)。
func Bucket(h uint64, p int) int {
	return int(h & (uint64(1)<<uint(p) - 1))
}

// Rho 取 w = h >> p 在 (64-p) 位字段内的秩：1 + 前导零个数；w==0 时为 64-p+1。
func Rho(h uint64, p int) uint8 {
	w := h >> uint(p)
	if w == 0 {
		return uint8(64 - p + 1)
	}
	return uint8(bits.LeadingZeros64(w) - p + 1)
}

package bloom

import (
	"encoding/binary"
	"hash/fnv"
)

// hashPair 计算元素的两个 64 位哈希。
//
// 用 FNV-1a（标准库 hash/fnv）算 h1；再把 h1 与固定常数异或后
// 二次哈希得到 h2，避免两个哈希相关。全程确定性：同一输入永远
// 得到同一输出，不依赖 map 迭代顺序，无随机种子。
//
// nil 与空切片写入哈希的字节序列相同，故为同一元素。
func hashPair(data []byte) (uint64, uint64) {
	h := fnv.New64a()
	_, _ = h.Write(data)
	h1 := h.Sum64()

	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], h1^0x9e3779b97f4a7c15)
	h.Reset()
	_, _ = h.Write(buf[:])
	h2 := h.Sum64()
	return h1, h2
}

// positions 返回元素的 k 个位下标（双哈希：pos_i = (h1 + i*h2) mod m）。
func (f *Filter) positions(data []byte) []uint64 {
	h1, h2 := hashPair(data)
	pos := make([]uint64, f.k)
	for i := uint64(0); i < f.k; i++ {
		pos[i] = (h1 + i*h2) % f.m
	}
	return pos
}

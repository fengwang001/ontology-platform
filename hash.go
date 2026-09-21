package ontology

import (
	"encoding/binary"
	"hash/fnv"
)

// 哈希方案：FNV-1a 64 位的双重哈希（Kirsch-Mitzenmacher）。
//
// 对元素 e 计算两个 64 位哈希 h1、h2，第 i 个探测位置为
//
//	g_i(e) = (h1 + i*h2) mod m,  i = 0..k-1
//
// h2 通过对 h1 的 8 字节大端表示再做一次 FNV-1a 得到，保证 h1、h2
// 都由元素内容唯一确定。FNV 是纯函数，不依赖 map 迭代顺序，也不依赖
// 任何随机种子（因此这里没有使用 hash/maphash，规避了其 Seed 的
// 不确定性问题），同一元素在任何进程、任何时刻得到的位集合完全相同。
//
// 空切片与 nil 写入哈希器时都是零字节，因此二者被视为同一个元素。
func hashPair(data []byte) (h1, h2 uint64) {
	h := fnv.New64a()
	_, _ = h.Write(data) // hash.Hash 的 Write 永不返回错误
	h1 = h.Sum64()

	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], h1)
	h.Reset()
	_, _ = h.Write(buf[:])
	h2 = h.Sum64()
	return h1, h2
}

// positions 返回元素应置位/检查的 k 个位下标。
func positions(data []byte, m, k uint) []uint {
	h1, h2 := hashPair(data)
	pos := make([]uint, k)
	for i := uint(0); i < k; i++ {
		pos[i] = uint((h1 + uint64(i)*h2) % uint64(m))
	}
	return pos
}

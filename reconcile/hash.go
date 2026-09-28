package reconcile

import (
	"encoding/binary"
	"hash/fnv"
)

// 本组件使用 FNV-1a 64 位作为组合哈希。哈希只用于对账时快速判断两个区间
// 是否“可能不同”，不承担密码学安全职责。
//
// 为了让空区间、不存在的键与值为 0 的键彼此可区分，三类摘要使用不同的
// 单字节域分离标签：
//   - tagEmpty：空区间（区间内没有任何已写入键）；
//   - tagAbsent：不存在的键（叶子位上键缺失）；
//   - tagLeaf：存在的键，后接该键值的 8 字节小端编码。
//
// 内部节点的摘要由其全部子区间的摘要顺序拼接后再做一次域分离哈希得到，
// 因此键在区间中的位置不同会产生不同的根哈希。
const (
	tagEmpty  byte = 0x00
	tagAbsent byte = 0x01
	tagLeaf   byte = 0x02
	tagNode   byte = 0x03
)

const uint64Len = 8

var fnvEmpty = func() uint64 {
	h := fnv.New64a()
	h.Write([]byte{tagEmpty})
	return h.Sum64()
}()

var fnvAbsent = func() uint64 {
	h := fnv.New64a()
	h.Write([]byte{tagAbsent})
	return h.Sum64()
}()

// leafHash 计算“存在的键”的叶子摘要；值为 0 与键缺失（fnvAbsent）
// 以及空区间（fnvEmpty）的摘要均不同。
func leafHash(value uint64) uint64 {
	h := fnv.New64a()
	h.Write([]byte{tagLeaf})
	var buf [uint64Len]byte
	binary.LittleEndian.PutUint64(buf[:], value)
	h.Write(buf[:])
	return h.Sum64()
}

// combineChildren 按子区间从左到右的固定顺序组合出父区间摘要。
// 顺序拼接保证相同内容落在不同子区间时哈希不同。
func combineChildren(children []uint64) uint64 {
	h := fnv.New64a()
	h.Write([]byte{tagNode})
	var buf [uint64Len]byte
	for _, child := range children {
		binary.LittleEndian.PutUint64(buf[:], child)
		h.Write(buf[:])
	}
	return h.Sum64()
}

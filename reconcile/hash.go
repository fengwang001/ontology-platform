package reconcile

import "encoding/binary"

const (
	fnvOffset64 = 14695981039346656037
	fnvPrime64  = 1099511628211

	// 叶子三种状态使用不同的域标签，确保“键不存在”、“值为零长”
	// 与“有非空值”即使在空输入下也产生不同哈希。
	leafTagAbsent = 0x01
	leafTagEmpty  = 0x02
	leafTagValue  = 0x03

	// 父节点域标签，与叶子域分离；再混入层级与区间起点，
	// 使结构不同但内容相似的区间无法相互冒充。
	nodeTagCombine = 0x10
)

func fnvWriteByte(h, b uint64) uint64 {
	h ^= b
	h *= fnvPrime64
	return h
}

func fnvWriteBytes(h uint64, p []byte) uint64 {
	for _, b := range p {
		h ^= uint64(b)
		h *= fnvPrime64
	}
	return h
}

func fnvWriteUint64(h, v uint64) uint64 {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], v)
	return fnvWriteBytes(h, buf[:])
}

// leafHash 计算单个键槽的叶子哈希。
// present=false 表示键不存在；present=true 且 value 为空表示存在一个
// 零长（零值）键；present=true 且 value 非空表示存在具体值。
func leafHash(key uint64, value []byte, present bool) uint64 {
	h := uint64(fnvOffset64)
	h = fnvWriteUint64(h, key)
	switch {
	case !present:
		h = fnvWriteByte(h, leafTagAbsent)
	case len(value) == 0:
		h = fnvWriteByte(h, leafTagEmpty)
	default:
		h = fnvWriteByte(h, leafTagValue)
		// 长度前缀消除“相邻值拼接”二义性。
		h = fnvWriteUint64(h, uint64(len(value)))
		h = fnvWriteBytes(h, value)
	}
	return h
}

// combineChildren 按子槽位的固定顺序组合出父区间哈希。
// hashes 的长度必须等于 fanout；空槽位本身的叶子哈希已天然
// 概括“不存在”，因此空区间不会被组合成零值或被忽略。
func combineChildren(level, base uint64, hashes []uint64) uint64 {
	h := uint64(fnvOffset64)
	h = fnvWriteByte(h, nodeTagCombine)
	h = fnvWriteUint64(h, uint64(level))
	h = fnvWriteUint64(h, base)
	h = fnvWriteUint64(h, uint64(len(hashes)))
	for _, child := range hashes {
		h = fnvWriteUint64(h, child)
	}
	return h
}

package ontology

// FNV-1a 64 位常量。
const (
	fnvOffsetBasis uint64 = 14695981039346656037
	fnvPrime       uint64 = 1099511628211
	// fnvOffsetBasis2 是第二个哈希使用的固定偏移基，
	// 由黄金比例常数派生，与第一个偏移基无关。
	fnvOffsetBasis2 uint64 = 14695981039346656037 ^ 0x9e3779b97f4a7c15
)

// fnv1a 以指定的偏移基对 data 计算 FNV-1a 64 位哈希。
// 结果完全由输入字节与偏移基决定，不涉及任何随机状态。
func fnv1a(data []byte, offset uint64) uint64 {
	h := offset
	for _, b := range data {
		h ^= uint64(b)
		h *= fnvPrime
	}
	return h
}

// mix64 是 splitmix64 的最终混合函数。FNV-1a 对熵集中在末尾
// 少数字节的输入雪崩不足，直接取模会导致位分布不均；经过
// mix64 后哈希值的每一位都充分混合。该函数是确定性的纯函数。
func mix64(z uint64) uint64 {
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// positions 返回元素 data 对应的 k 个位下标（均在 [0, m) 内）。
// 采用 Kirsch-Mitzenmacher 双重哈希：第 i 个位置为
// (h1 + i*h2) mod m，只需两次完整哈希即可模拟 k 个哈希函数。
func positions(data []byte, m, k uint64) []uint64 {
	h1 := mix64(fnv1a(data, fnvOffsetBasis))
	h2 := mix64(fnv1a(data, fnvOffsetBasis2))
	// h2 为奇数可保证双重哈希扫过更多位置，降低退化概率。
	h2 |= 1
	pos := make([]uint64, k)
	for i := uint64(0); i < k; i++ {
		pos[i] = (h1 + i*h2) % m
	}
	return pos
}

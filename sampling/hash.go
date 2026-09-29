package sampling

// 采用 FNV-1a（Fowler–Noll–Vo，32 位变体）哈希：
//   - 纯函数、与运行时 map 哈希无关，多个实例/进程对同一键得到同一结果；
//   - 无需外部依赖，Go 标准库 hash/fnv 也是同一算法，可直接对照。
//
// 取模 BucketRange 将键稳定映射到固定桶区间 [0, BucketRange)。
const (
	fnvOffset32 = 2166136261
	fnvPrime32  = 16777619
)

// BucketRange 是桶空间大小：采样率以整数万分比表示，取值 [0, 10000]，
// 键被采样当且仅当其桶值严格小于采样率。
const BucketRange = 10000

// bucket 返回键在固定范围 [0, BucketRange) 内的桶值。
func bucket(key string) uint32 {
	h := uint32(fnvOffset32)
	for i := 0; i < len(key); i++ {
		h ^= uint32(key[i])
		h *= fnvPrime32
	}
	return h % BucketRange
}

// sampledAt 是与实例状态无关的朴素判定：桶值严格小于采样率即采出。
func sampledAt(bucketValue, rate uint32) bool {
	return bucketValue < rate
}

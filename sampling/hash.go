package sampling

import "hash/fnv"

// hashKey 使用 FNV-1a 64 位哈希把键映射为稳定的无符号整数。
// FNV-1a 为确定性算法：同一键在任意进程、任意实例上得到同一结果。
func hashKey(key string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(key))
	return h.Sum64()
}

// Bucket 返回键映射到的桶值，取值区间 [0, BucketCount)。
//
// 纯函数：不依赖采样器状态、不依赖时间与随机源，因此跨实例一致、可复现。
func Bucket(key string) int {
	return int(hashKey(key) % uint64(BucketCount))
}

// sampledByBucket 是朴素采样判定：桶值严格小于采样率时采出。
// rate=0 时不采任何键；rate=BucketCount 时采全部键；
// 边界桶 bucket == rate 恰好不被采（严格小于）。
func sampledByBucket(bucket, rate int) bool {
	return bucket < rate
}

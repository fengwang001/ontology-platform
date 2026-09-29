package rebalance

import "hash/fnv"

// hashKey 返回键的确定性 64 位 FNV-1a 哈希。同一键在任意进程、
// 任意时间得到的值都相同，从而保证归属可复现。
func hashKey(key string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(key))
	return h.Sum64()
}

// ownerOf 返回键在 n 个分区下的归属分区：hash(key) mod n。
func ownerOf(key string, n int) int {
	return int(hashKey(key) % uint64(n))
}

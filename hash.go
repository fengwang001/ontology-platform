package ontology

import "hash/fnv"

// defaultHash 是确定性哈希：FNV-1a 64 再经 murmur3 fmix64 终混，
// 弥补 FNV 高位扩散不足导致的短串聚集。不依赖任何随机种子，
// 因此同一进程乃至不同进程内重建的环排布完全一致。
func defaultHash(b []byte) uint64 {
	h := fnv.New64a()
	_, _ = h.Write(b)
	return fmix64(h.Sum64())
}

func fmix64(h uint64) uint64 {
	h ^= h >> 33
	h *= 0xff51afd7ed558ccd
	h ^= h >> 33
	h *= 0xc4ceb9fe1a85ec53
	h ^= h >> 33
	return h
}

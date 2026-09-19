package ontology

import "sync"

// shardCount is the number of independent maps the tenants are spread
// across. Each shard has its own lock, and each bucket inside a shard has
// its own lock too, so concurrent Allow calls for different tenants never
// block each other on a single global lock.
const shardCount = 64

// shard is one stripe of the tenant map.
type shard struct {
	mu      sync.RWMutex
	buckets map[string]*bucket
}

// hashString is FNV-1a (64-bit), kept dependency-free.
func hashString(s string) uint64 {
	const (
		offset64 = uint64(14695981039346656037)
		prime64  = uint64(1099511628211)
	)
	h := offset64
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime64
	}
	return h
}

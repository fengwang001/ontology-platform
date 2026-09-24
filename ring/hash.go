package ring

import (
	"encoding/binary"
	"hash/fnv"
)

// HashKey maps a key to its position on the ring. It uses FNV-1a,
// which is fully deterministic and carries no random seed.
func HashKey(key string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(key))
	return mix64(h.Sum64())
}

// vnodeHash positions the index-th virtual node of node on the ring.
func vnodeHash(node string, index int) uint64 {
	h := fnv.New64a()
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(index))
	h.Write(buf[:])
	h.Write([]byte(node))
	return mix64(h.Sum64())
}

// mix64 is a murmur3-style finalizer. FNV-1a alone does not avalanche
// differences in trailing input bytes, which would cluster virtual
// nodes of structured IDs ("node-00", "node-01", ...) into tiny arcs.
func mix64(x uint64) uint64 {
	x ^= x >> 33
	x *= 0xff51afd7ed558ccd
	x ^= x >> 33
	x *= 0xc4ceb9fe1a85ec53
	x ^= x >> 33
	return x
}

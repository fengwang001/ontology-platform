package ontology

import (
	"encoding/binary"
	"hash/fnv"
)

// mix64 is the splitmix64 finalizer. FNV-1a diffuses trailing input
// bytes poorly (differing only in the last byte yields clustered
// positions), so every ring position passes through this finalizer to
// get full 64-bit avalanche. It is a fixed bijection: fully
// deterministic, no random seed.
func mix64(x uint64) uint64 {
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return x
}

// HashKey hashes a key to its position on the ring. It uses FNV-1a,
// which is fully deterministic: no random seed is involved, so the same
// key always maps to the same position, in any process, on any run.
func HashKey(key string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return mix64(h.Sum64())
}

// vnodeHash computes the ring position of the i-th virtual node of id.
func vnodeHash(id string, i int) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(id))
	_, _ = h.Write([]byte{0})
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], uint64(i))
	_, _ = h.Write(buf[:])
	return mix64(h.Sum64())
}

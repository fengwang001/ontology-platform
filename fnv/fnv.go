// Package fnv implements the 32-bit FNV-1a hash primitives used by the
// Merkle tree: the raw hash, the domain-separated leaf hash, and the
// domain-separated interior-node combine.
package fnv

import "encoding/binary"

const (
	offset32 = 0x811C9DC5
	prime32  = 0x01000193
)

// FNV32 hashes data with 32-bit FNV-1a: h starts at the offset basis and
// for each byte h = (h XOR byte) * prime, with uint32 wraparound.
func FNV32(data []byte) uint32 {
	h := uint32(offset32)
	for _, b := range data {
		h = (h ^ uint32(b)) * prime32
	}
	return h
}

// LeafHash is fnv32(0x00 || data): a 0x00 domain-separation byte followed
// by the leaf data.
func LeafHash(data []byte) uint32 {
	buf := make([]byte, 0, len(data)+1)
	buf = append(buf, 0x00)
	buf = append(buf, data...)
	return FNV32(buf)
}

// Combine is fnv32(0x01 || l || r): a 0x01 domain-separation byte followed
// by l and r, each encoded as 4 bytes big-endian.
func Combine(l, r uint32) uint32 {
	var buf [9]byte
	buf[0] = 0x01
	binary.BigEndian.PutUint32(buf[1:5], l)
	binary.BigEndian.PutUint32(buf[5:9], r)
	return FNV32(buf[:])
}

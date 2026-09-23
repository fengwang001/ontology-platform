// Package hashfam derives a deterministic family of hash functions from
// construction parameters only: no random source, clock time or map order.
package hashfam

import (
	"encoding/binary"
	"errors"
	"hash/fnv"
)

// ErrBadDepth is returned when the requested family size is not positive.
var ErrBadDepth = errors.New("hashfam: depth must be positive")

// Func maps a key to a raw 64-bit hash. The caller reduces it modulo width.
type Func func(key string) uint64

// New deterministically derives d independent hash functions. Row i salts
// FNV-1a with the fixed 8-byte little-endian encoding of i, so the same
// parameters and key always produce the same hashes in every process.
func New(d int) ([]Func, error) {
	if d <= 0 {
		return nil, ErrBadDepth
	}
	fns := make([]Func, d)
	for i := 0; i < d; i++ {
		var salt [8]byte
		binary.LittleEndian.PutUint64(salt[:], uint64(i))
		fns[i] = func(key string) uint64 {
			h := fnv.New64a()
			h.Write(salt[:])
			h.Write([]byte(key))
			return h.Sum64()
		}
	}
	return fns, nil
}

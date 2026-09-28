package hotcount

import "hash/fnv"

// fnvNew returns a fresh 64-bit FNV-1a hasher.
func fnvNew() interface {
	Write([]byte) (int, error)
	Sum64() uint64
} {
	return fnv.New64a()
}

// Package bloom implements a Bloom filter over the bits package.
package bloom

import (
	"encoding/binary"
	"errors"
	"hash/fnv"
	"math"
	"sync/atomic"

	"ontology/bits"
)

// ErrBadParam is returned by New when n == 0 or p is not in (0, 1).
var ErrBadParam = errors.New("bloom: bad parameter")

// Filter is a Bloom filter. The zero value is an empty, safe filter.
type Filter struct {
	bits  *bits.Array
	m     uint64 // derived bit-array size
	k     uint64 // derived hash count
	seed  uint64
	reads atomic.Uint64 // bit reads of the last MaybeContains call
}

// New derives m and k from n expected elements and target false-positive
// rate p: m = -n·ln p / (ln2)^2, k = (m/n)·ln2 (see NOTES.md).
func New(n uint64, p float64, seed uint64) (*Filter, error) {
	if n == 0 || p <= 0 || p >= 1 {
		return nil, ErrBadParam
	}
	m := uint64(math.Ceil(-float64(n) * math.Log(p) / (math.Ln2 * math.Ln2)))
	k := uint64(math.Round(float64(m) / float64(n) * math.Ln2))
	if k == 0 {
		k = 1
	}
	return &Filter{bits: bits.New(m), m: m, k: k, seed: seed}, nil
}

// M returns the derived bit-array size.
func (f *Filter) M() uint64 { return f.m }

// K returns the derived hash count.
func (f *Filter) K() uint64 { return f.k }

// Reads returns the number of bit reads of the last MaybeContains call.
func (f *Filter) Reads() uint64 { return f.reads.Load() }

// hash is deterministic for a fixed seed; salt derives independent hashes.
func (f *Filter) hash(b []byte, salt uint64) uint64 {
	var s [8]byte
	binary.LittleEndian.PutUint64(s[:], f.seed+salt)
	h := fnv.New64a()
	h.Write(s[:])
	h.Write(b)
	return h.Sum64()
}

// Add inserts b. It is a no-op on an empty (zero-value) filter.
func (f *Filter) Add(b []byte) {
	if f.m == 0 {
		return
	}
	h1, h2 := f.hash(b, 0), f.hash(b, 1)
	for i := uint64(0); i < f.k; i++ {
		f.bits.Set((h1 + i*h2) % f.m)
	}
}

// MaybeContains reports whether b was probably added. It never reports
// false for an added value, and reads exactly k bits per call.
func (f *Filter) MaybeContains(b []byte) bool {
	if f.m == 0 || f.k == 0 {
		f.reads.Store(0)
		return false
	}
	h1, h2 := f.hash(b, 0), f.hash(b, 1)
	ok := true
	for i := uint64(0); i < f.k; i++ {
		ok = f.bits.Get((h1+i*h2)%f.m) && ok
	}
	f.reads.Store(f.k)
	return ok
}

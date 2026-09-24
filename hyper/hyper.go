// Package hyper implements a seeded random-hyperplane hash family.
package hyper

import (
	"math/rand"
	"sync/atomic"

	"ontology/vec"
)

// Family is a set of bits random hyperplanes in dims dimensions.
type Family struct {
	dims, bits int
	planes     [][]float64 // [bit][dim]
	computed   atomic.Uint64
}

// New builds a family deterministically from an explicit seed.
func New(seed int64, dims, bits int) *Family {
	if dims < 1 || bits < 1 || bits > 64 {
		panic("hyper: bad dims/bits")
	}
	r := rand.New(rand.NewSource(seed))
	planes := make([][]float64, bits)
	for i := range planes {
		p := make([]float64, dims)
		for j := range p {
			p[j] = r.NormFloat64()
		}
		planes[i] = p
	}
	return &Family{dims: dims, bits: bits, planes: planes}
}

// FromPlanes rebuilds a family from explicit plane normals (used by persist).
func FromPlanes(planes [][]float64) *Family {
	return &Family{dims: len(planes[0]), bits: len(planes), planes: planes}
}

// Dims returns the vector dimension the family operates on.
func (f *Family) Dims() int { return f.dims }

// Bits returns the signature width in bits.
func (f *Family) Bits() int { return f.bits }

// Planes returns the plane normals, [bit][dim].
func (f *Family) Planes() [][]float64 { return f.planes }

// Signature hashes v into a bits-wide signature. A dot product of exactly
// zero (e.g. the zero vector) falls into the positive half-space: bit = 1.
func (f *Family) Signature(v vec.Vec) uint64 {
	var sig uint64
	for i := 0; i < f.bits; i++ {
		dot := 0.0
		p := f.planes[i]
		for j := 0; j < f.dims; j++ {
			dot += p[j] * v[j]
		}
		f.computed.Add(1)
		if dot >= 0 {
			sig |= 1 << uint(i)
		}
	}
	return sig
}

// Computed returns how many hyperplane dot products this family has run.
func (f *Family) Computed() uint64 { return f.computed.Load() }

// ResetComputed zeroes the hash-computation counter.
func (f *Family) ResetComputed() { f.computed.Store(0) }

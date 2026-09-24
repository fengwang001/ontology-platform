// Package hyper implements a seeded random-hyperplane LSH family.
package hyper

import (
	"math/rand"

	"ontology/vec"
)

// MaxBits is the largest usable signature width.
const MaxBits = 64

// Family is a set of random hyperplanes sharing one seed.
type Family struct {
	dim     int
	bits    int
	planes  [][]float64
	hashOps int64
}

// New builds a family deterministically from an explicit seed.
func New(seed int64, dim, bits int) *Family {
	if dim < 1 || bits < 1 || bits > MaxBits {
		panic("hyper: bad dim or bits")
	}
	rng := rand.New(rand.NewSource(seed))
	planes := make([][]float64, bits)
	for i := range planes {
		p := make([]float64, dim)
		for j := range p {
			p[j] = rng.NormFloat64()
		}
		planes[i] = p
	}
	return &Family{dim: dim, bits: bits, planes: planes}
}

// FromPlanes rebuilds a family from persisted hyperplane normals.
func FromPlanes(planes [][]float64) *Family {
	f := &Family{bits: len(planes), planes: planes}
	if len(planes) > 0 {
		f.dim = len(planes[0])
	}
	return f
}

// Dim returns the vector dimension the family was built for.
func (f *Family) Dim() int { return f.dim }

// Bits returns the signature width in bits.
func (f *Family) Bits() int { return f.bits }

// Planes exposes the hyperplane normals for persistence.
func (f *Family) Planes() [][]float64 { return f.planes }

// HashOps reports how many dot products were computed for hashing.
func (f *Family) HashOps() int64 { return f.hashOps }

// Signature maps v to its bit string: bit i is 1 iff <v, plane_i> >= 0,
// so the zero vector deterministically maps to the all-ones signature.
func (f *Family) Signature(v vec.Vec) uint64 {
	var sig uint64
	for i, p := range f.planes {
		f.hashOps++
		if vec.Dot(v, p) >= 0 {
			sig |= 1 << uint(i)
		}
	}
	return sig
}

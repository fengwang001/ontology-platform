// Package hyper builds seeded random hyperplane families and computes LSH signatures.
package hyper

import (
	"errors"
	"math"
	"math/rand"

	"ontology/vec"
)

// ErrZeroPlane reports a degenerate all-zero normal at (table, bit).
var ErrZeroPlane = errors.New("hyper: all-zero plane normal")

// Family is L tables of b hyperplanes in dimension d.
type Family struct {
	Dim    int
	Bits   int
	Tables int
	Seed   int64
	// Planes[t][k] is the k-th normal of table t, each length Dim.
	Planes [][]vec.Vec
	// HashCount counts inner-product sign evaluations: vectors*tables*bits in indexing.
	HashCount int64
}

// NewFamily deterministically generates tables*bits normals from a seeded RNG.
func NewFamily(dim, bits, tables int, seed int64) *Family {
	rng := rand.New(rand.NewSource(seed))
	planes := make([][]vec.Vec, tables)
	for t := 0; t < tables; t++ {
		planes[t] = make([]vec.Vec, bits)
		for k := 0; k < bits; k++ {
			n := make(vec.Vec, dim)
			for d := 0; d < dim; d++ {
				n[d] = rng.NormFloat64()
			}
			planes[t][k] = n
		}
	}
	return &Family{Dim: dim, Bits: bits, Tables: tables, Seed: seed, Planes: planes}
}

// FromPlanes wraps externally supplied normals (used when loading an index).
func FromPlanes(planes [][]vec.Vec, seed int64) (*Family, error) {
	if len(planes) == 0 || len(planes[0]) == 0 {
		return nil, ErrZeroPlane
	}
	dim := len(planes[0][0])
	for t := range planes {
		for k := range planes[t] {
			if len(planes[t][k]) != dim {
				return nil, vec.DimError{Want: dim, Got: len(planes[t][k])}
			}
			allZero := true
			for _, v := range planes[t][k] {
				if v != 0 {
					allZero = false
				}
			}
			if allZero {
				return nil, ZeroPlaneError{Table: t, Bit: k}
			}
		}
	}
	return &Family{
		Dim: dim, Bits: len(planes[0]), Tables: len(planes),
		Seed: seed, Planes: planes,
	}, nil
}

// ZeroPlaneError locates a degenerate normal; errors.Is(f, ErrZeroPlane).
type ZeroPlaneError struct{ Table, Bit int }

func (e ZeroPlaneError) Error() string {
	return "hyper: all-zero plane normal at table/bit"
}
func (e ZeroPlaneError) Is(target error) bool { return target == ErrZeroPlane }

// Signature returns the b-bit signature of x in table t.
// bit k = 1 iff <x, normal> > 0; inner product 0 (incl. zero vector) maps to 0.
func (f *Family) Signature(t int, x vec.Vec) (uint64, error) {
	if len(x) != f.Dim {
		return 0, vec.DimError{Want: f.Dim, Got: len(x)}
	}
	var sig uint64
	for k := 0; k < f.Bits; k++ {
		d, err := vec.Dot(x, f.Planes[t][k])
		if err != nil {
			return 0, err
		}
		f.HashCount++
		if d > 0 {
			sig |= 1 << uint(k)
		}
	}
	return sig, nil
}

// CollisionProbe is the analytic single-bit collision probability 1-theta/pi.
func CollisionProbe(theta float64) float64 { return 1 - theta/math.Pi }

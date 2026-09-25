// Package hyper implements a reproducible random-hyperplane LSH family.
package hyper

import (
	"math/rand"
	"sync/atomic"

	"ontology/vec"
)

// Family is tables×bits random hyperplanes in dim dimensions.
type Family struct {
	dim     int
	bits    int
	tables  int
	normals [][][]float64 // [table][bit][dim]
	hashOps atomic.Int64  // inner products computed for signatures
}

// New builds a family from an explicit seed. Generation order is fixed
// (table, bit, component), so equal seeds give byte-identical normals.
func New(seed int64, dim, bits, tables int) *Family {
	rng := rand.New(rand.NewSource(seed))
	normals := make([][][]float64, tables)
	for t := range normals {
		normals[t] = make([][]float64, bits)
		for h := range normals[t] {
			n := make([]float64, dim)
			for i := range n {
				n[i] = rng.NormFloat64()
			}
			normals[t][h] = n
		}
	}
	return &Family{dim: dim, bits: bits, tables: tables, normals: normals}
}

// NewFrom rebuilds a family from loaded normals (used by persist).
func NewFrom(normals [][][]float64) *Family {
	f := &Family{tables: len(normals), normals: normals}
	if len(normals) > 0 {
		f.bits = len(normals[0])
		if len(normals[0]) > 0 {
			f.dim = len(normals[0][0])
		}
	}
	return f
}

func (f *Family) Dim() int    { return f.dim }
func (f *Family) Bits() int   { return f.bits }
func (f *Family) Tables() int { return f.tables }

// Normals exposes the hyperplane normals for persistence.
func (f *Family) Normals() [][][]float64 { return f.normals }

// HashOps reports how many inner products were computed for signatures.
func (f *Family) HashOps() int64 { return f.hashOps.Load() }

// Sign returns one b-bit signature per table. Bit h is 1 iff the inner
// product with normal h is > 0; exactly 0 (e.g. the zero vector) yields 0.
func (f *Family) Sign(v vec.Vector) ([]uint64, error) {
	if err := vec.Check(v, f.dim); err != nil {
		return nil, err
	}
	sigs := make([]uint64, f.tables)
	for t := 0; t < f.tables; t++ {
		var sig uint64
		for h := 0; h < f.bits; h++ {
			f.hashOps.Add(1)
			if vec.Dot(v, f.normals[t][h]) > 0 {
				sig |= 1 << uint(h)
			}
		}
		sigs[t] = sig
	}
	return sigs, nil
}

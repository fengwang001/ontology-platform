// Package hyper provides seeded random hyperplane families and b-bit
// sign signatures (random hyperplane LSH, Charikar 2002).
package hyper

import (
	"errors"
	"math/rand"

	"ontology/vec"
)

// Family holds L tables of b random hyperplanes in dimension d.
type Family struct {
	Dim    int
	Bits   int
	Tables int
	Seed   int64
	// planes[table][bit] is the normal vector of one hyperplane.
	planes [][]vec.Vector
}

// ErrDegenerateHyper marks an all-zero normal (cannot split space).
var ErrDegenerateHyper = errors.New("hyper: degenerate all-zero normal")

// DegenerateError identifies table index and bit position; wraps ErrDegenerateHyper.
type DegenerateError struct {
	Table int
	Bit   int
}

func (e DegenerateError) Error() string {
	return "hyper: degenerate normal at table " + itoa(e.Table) +
		" bit " + itoa(e.Bit)
}

func (e DegenerateError) Is(target error) bool { return target == ErrDegenerateHyper }

// NewFamily deterministically generates L*b Gaussian normals from seed.
// Generation order is fixed: table, then bit, then component, independent
// of any indexed data, so the same seed reproduces byte-identical planes.
func NewFamily(dim, bits, tables int, seed int64) *Family {
	f := &Family{Dim: dim, Bits: bits, Tables: tables, Seed: seed,
		planes: make([][]vec.Vector, tables)}
	rng := rand.New(rand.NewSource(seed))
	for t := 0; t < tables; t++ {
		f.planes[t] = make([]vec.Vector, bits)
		for j := 0; j < bits; j++ {
			p := make(vec.Vector, dim)
			for k := range p {
				p[k] = rng.NormFloat64()
			}
			f.planes[t][j] = p
		}
	}
	return f
}

// Planes returns the normal vectors of one table (index j is bit position).
func (f *Family) Planes(table int) []vec.Vector { return f.planes[table] }

// AllPlanes returns every table's normals (for persistence).
func (f *Family) AllPlanes() [][]vec.Vector { return f.planes }

// FromPlanes rebuilds a family from explicit normals and validates them.
func FromPlanes(dim, bits int, seed int64, planes [][]vec.Vector) (*Family, error) {
	for t, tbl := range planes {
		if len(tbl) != bits {
			return nil, errors.New("hyper: bit count mismatch")
		}
		for j, p := range tbl {
			if len(p) != dim {
				return nil, vec.DimError{Expected: dim, Actual: len(p)}
			}
			zero := true
			for _, x := range p {
				if x != 0 {
					zero = false
					break
				}
			}
			if zero {
				return nil, DegenerateError{Table: t, Bit: j}
			}
		}
	}
	return &Family{Dim: dim, Bits: bits, Tables: len(planes), Seed: seed,
		planes: planes}, nil
}

// Signature returns the b-bit sign signature of v in one table.
// Rule: bit j = 1 iff dot(normal, v) > 0; dot == 0 (incl. zero vector) -> 0.
func (f *Family) Signature(table int, v vec.Vector) (uint64, error) {
	if err := vec.CheckDim(f.Dim, len(v)); err != nil {
		return 0, err
	}
	var sig uint64
	for j, h := range f.planes[table] {
		d, _ := vec.Dot(h, v)
		if d > 0 {
			sig |= 1 << uint(j)
		}
	}
	return sig, nil
}

// Equal reports whether two families have byte-identical normals.
func (f *Family) Equal(o *Family) bool {
	if f.Dim != o.Dim || f.Bits != o.Bits || f.Tables != o.Tables {
		return false
	}
	for t := range f.planes {
		for j := range f.planes[t] {
			a, b := f.planes[t][j], o.planes[t][j]
			for k := range a {
				if a[k] != b[k] {
					return false
				}
			}
		}
	}
	return true
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

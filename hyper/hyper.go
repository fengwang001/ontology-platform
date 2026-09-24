// Package hyper generates reproducible random hyperplane families and
// computes b-bit locality-sensitive signatures.
package hyper

import (
	"errors"
	"math"
	"math/rand"

	"ontology/vec"
)

// Family is L tables of b hyperplanes in dimension d.
type Family struct {
	Dim    int
	Tables int
	Bits   int
	// Normals[table][bit] is the hyperplane normal.
	Normals [][]vec.Vec
}

// ErrDegenerateHyperplane marks an all-zero normal (cannot partition space).
var ErrDegenerateHyperplane = errors.New("hyper: degenerate all-zero hyperplane")

type degenerateErr struct{ table, bit int }

func (e degenerateErr) Error() string {
	return "hyper: degenerate all-zero hyperplane at table " +
		itoa(e.table) + " bit " + itoa(e.bit)
}
func (e degenerateErr) Is(t error) bool { return t == ErrDegenerateHyperplane }

// TableBit extracts the table and bit indices from a degeneracy error.
func TableBit(err error) (table, bit int, ok bool) {
	var d degenerateErr
	if errors.As(err, &d) {
		return d.table, d.bit, true
	}
	return 0, 0, false
}

// New deterministically builds a family from an explicit seed.
func New(dim, tables, bits int, seed int64) (*Family, error) {
	if dim < 1 || tables < 1 || bits < 1 || bits > 64 {
		return nil, errors.New("hyper: invalid parameters")
	}
	f := &Family{Dim: dim, Tables: tables, Bits: bits}
	rng := rand.New(rand.NewSource(seed))
	f.Normals = make([][]vec.Vec, tables)
	for t := 0; t < tables; t++ {
		f.Normals[t] = make([]vec.Vec, bits)
		for b := 0; b < bits; b++ {
			n := make(vec.Vec, dim)
			for i := range n {
				n[i] = rng.NormFloat64()
			}
			f.Normals[t][b] = n
			if isZero(n) {
				return nil, degenerateErr{table: t, bit: b}
			}
		}
	}
	return f, nil
}

// FromNormals validates a family reconstructed from disk, detecting zero normals.
func FromNormals(normals [][]vec.Vec) (*Family, error) {
	if len(normals) == 0 || len(normals[0]) == 0 {
		return nil, errors.New("hyper: empty family")
	}
	dim := len(normals[0][0])
	for t := range normals {
		for b := range normals[t] {
			n := normals[t][b]
			if len(n) != dim {
				return nil, errors.New("hyper: ragged normals")
			}
			if isZero(n) {
				return nil, degenerateErr{table: t, bit: b}
			}
		}
	}
	return &Family{Dim: dim, Tables: len(normals), Bits: len(normals[0]), Normals: normals}, nil
}

// Signature computes the b-bit signature of x for one table.
// Bit j is 1 iff dot(n_j, x) > 0; dot == 0 (incl. zero vector) maps to 0.
func (f *Family) Signature(table int, x vec.Vec) (uint64, error) {
	if err := vec.Validate(x); err != nil {
		return 0, err
	}
	if len(x) != f.Dim {
		return 0, vec.DimMismatchError(f.Dim, len(x))
	}
	var sig uint64
	for j, n := range f.Normals[table] {
		p, err := vec.Dot(n, x)
		if err != nil {
			return 0, err
		}
		if p > 0 {
			sig |= 1 << uint(j)
		}
	}
	return sig, nil
}

func isZero(n vec.Vec) bool {
	for _, v := range n {
		if v != 0 || math.IsNaN(v) {
			return false
		}
	}
	return true
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0'+n%10)
		n /= 10
	}
	return string(b[i:])
}

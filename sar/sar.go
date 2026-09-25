// Package sar implements fixed-width serial number arithmetic: the
// four-state wrap-around comparison and the modulo-M forward distance.
// It depends on no other package in this module.
package sar

import "errors"

// Rel is the four-state relative order of two serial numbers.
type Rel int8

const (
	// Equal means the two values are the same residue.
	Equal Rel = iota
	// Less means a lies strictly before b within the forward half circle.
	Less
	// Incomparable is the half-circle boundary d == M/2.
	Incomparable
	// Greater means a lies strictly after b (more than a half circle ahead).
	Greater
)

func (r Rel) String() string {
	switch r {
	case Equal:
		return "Equal"
	case Less:
		return "Less"
	case Incomparable:
		return "Incomparable"
	default:
		return "Greater"
	}
}

// Decidable sentinel errors, shared by every layer so callers can use
// errors.Is regardless of which package rejected the operation.
var (
	// ErrWidth: N is outside 1..63.
	ErrWidth = errors.New("sar: invalid width N (must be 1 <= N <= 63)")
	// ErrOutOfRange: a serial number is >= M.
	ErrOutOfRange = errors.New("sar: serial number out of range (s >= M)")
	// ErrIncomparable: the half-circle boundary d == M/2 was hit.
	ErrIncomparable = errors.New("sar: half-circle distance is incomparable")
	// ErrGreater: the new serial number is behind last (stale/rewind).
	ErrGreater = errors.New("sar: serial number is behind (greater/rewind)")
)

// Arith is a pure, immutable width-N serial number arithmetic table.
type Arith struct {
	n    int
	m    uint64 // 1 << N
	half uint64 // M / 2
	mask uint64 // M - 1
}

// New returns the arithmetic table for width N; N must be 1..63.
func New(N int) (*Arith, error) {
	if N < 1 || N > 63 {
		return nil, ErrWidth
	}
	m := uint64(1) << uint(N)
	return &Arith{n: N, m: m, half: m >> 1, mask: m - 1}, nil
}

// Width reports N.
func (a *Arith) Width() int { return a.n }

// Mod reports M = 1 << N.
func (a *Arith) Mod() uint64 { return a.m }

// Half reports M/2.
func (a *Arith) Half() uint64 { return a.half }

// Mask reports M-1, the bit mask of the N low bits.
func (a *Arith) Mask() uint64 { return a.mask }

// Valid reports whether v is a legal serial number residue.
func (a *Arith) Valid(v uint64) bool { return v < a.m }

// CheckValid returns ErrOutOfRange when v >= M.
func (a *Arith) CheckValid(v uint64) error {
	if v >= a.m {
		return ErrOutOfRange
	}
	return nil
}

// Diff returns the unsigned modulo-M forward distance d = (b - a) & (M-1).
// a and b are required to be valid residues (< M).
func (a *Arith) Diff(x, y uint64) uint64 { return (y - x) & a.mask }

// Classify maps a forward distance to its four-state relation:
// d==0 Equal; 0<d<M/2 Less; d==M/2 Incomparable; d>M/2 Greater.
func (a *Arith) Classify(d uint64) Rel {
	switch {
	case d == 0:
		return Equal
	case d < a.half:
		return Less
	case d == a.half:
		return Incomparable
	default:
		return Greater
	}
}

// Cmp compares two valid residues: the result is the relation of a to b,
// i.e. Less means a is before b.
func (a *Arith) Cmp(x, y uint64) Rel {
	return a.Classify(a.Diff(x, y))
}

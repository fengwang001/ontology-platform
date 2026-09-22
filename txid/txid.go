// Package txid provides monotonically increasing transaction identifiers.
//
// A zero TxID is never a valid identifier. Values never wrap around: once the
// counter reaches the maximum uint64 the source reports ErrExhausted instead
// of returning zero or wrapping.
package txid

import "errors"

// TxID is a transaction identifier. The zero value is invalid.
type TxID uint64

// ErrIllegal is returned (or used as the cause of a panic) when a zero TxID
// is supplied where a valid identifier is required.
var ErrIllegal = errors.New("txid: zero is not a valid transaction id")

// ErrExhausted is returned by Source.Next when no further identifier can be
// issued without wrapping.
var ErrExhausted = errors.New("txid: transaction id space exhausted")

// Valid reports whether the identifier is non-zero.
func (t TxID) Valid() bool { return t != 0 }

// Before reports whether t < other.
func (t TxID) Before(other TxID) bool {
	if !t.Valid() || !other.Valid() {
		panic(ErrIllegal)
	}
	return uint64(t) < uint64(other)
}

// BeforeOrEqual reports whether t <= other.
func (t TxID) BeforeOrEqual(other TxID) bool {
	if !t.Valid() || !other.Valid() {
		panic(ErrIllegal)
	}
	return uint64(t) <= uint64(other)
}

// Max returns the larger of two identifiers.
func Max(a, b TxID) TxID {
	if !a.Valid() {
		return b
	}
	if !b.Valid() {
		return a
	}
	if a.Before(b) {
		return b
	}
	return a
}

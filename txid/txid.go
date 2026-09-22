// Package txid defines monotonic, non-wrapping transaction identifiers.
//
// A TxID is a dense, strictly increasing sequence number. The zero value is
// reserved as "no transaction" and is never handed out. Comparison is plain
// unsigned ordering: identifiers never wrap around; the allocator refuses to
// mint a new value once the space is exhausted rather than reusing a number.
package txid

// TxID is a monotonic transaction identifier. Zero is invalid.
type TxID uint64

// Zero is the invalid sentinel returned by the zero value of TxID.
const Zero TxID = 0

// Valid reports whether the identifier was minted by an allocator.
func (t TxID) Valid() bool { return t > Zero }

// Before reports whether t orders strictly earlier than other.
func (t TxID) Before(other TxID) bool { return t < other }

// After reports whether t orders strictly later than other.
func (t TxID) After(other TxID) bool { return t > other }

// Min returns the smaller of two identifiers.
func Min(a, b TxID) TxID {
	if a < b {
		return a
	}
	return b
}

// Package txid provides monotonic, non-wrapping transaction identifiers.
// The zero value is invalid; numbers are obtained only from an injected Source.
package txid

import "errors"

// TxID is a monotonically increasing transaction number.
type TxID uint64

// Valid reports whether the id is non-zero.
func (t TxID) Valid() bool { return t != 0 }

// Before reports whether t is strictly older than other.
func (t TxID) Before(other TxID) bool { return t < other }

// Source hands out never-reused, strictly increasing ids.
// It is safe for concurrent use.
type Source struct {
	next uint64
}

// ErrExhausted is returned when the id space is full (no wrap-around).
var ErrExhausted = errors.New("txid: id space exhausted")

// NewSource creates a source whose first issued id is 1.
func NewSource() *Source { return &Source{next: 1} }

// Next issues the following id. It never wraps to zero.
func (s *Source) Next() (TxID, error) {
	if s.next == 0 {
		return 0, ErrExhausted
	}
	id := s.next
	s.next++
	return TxID(id), nil
}

// Peek returns the id the next call to Next would issue, without consuming it.
func (s *Source) Peek() TxID { return TxID(s.next) }

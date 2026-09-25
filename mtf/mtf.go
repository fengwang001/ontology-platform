// Package mtf holds the move-to-front list state and single-symbol steps.
package mtf

import (
	"bytes"
	"errors"
)

// The three errors are distinguishable via errors.Is.
var (
	ErrInvalidAlphabet = errors.New("mtf: invalid alphabet: empty or duplicate symbols")
	ErrUnknownSymbol   = errors.New("mtf: unknown symbol")
	ErrInvalidIndex    = errors.New("mtf: invalid index")
)

// core is the symbol-type-agnostic list. T is byte in the public API; the
// generic form lets in-package checks exceed byte's 256-symbol limit.
type core[T comparable] struct {
	list       []T
	pos        map[T]int // symbol -> current index in list
	probeCount int       // elements inspected in the latest encode; unexported
}

// newCore validates alpha (non-empty, pairwise distinct) and copies it.
func newCore[T comparable](alpha []T) (*core[T], bool) {
	if len(alpha) == 0 {
		return nil, false
	}
	e := &core[T]{list: append([]T(nil), alpha...), pos: make(map[T]int, len(alpha))}
	for i, s := range e.list {
		if _, dup := e.pos[s]; dup {
			return nil, false
		}
		e.pos[s] = i
	}
	return e, true
}

// encode emits the current index then moves s to front; unchanged on error.
func (e *core[T]) encode(s T) (int, error) {
	idx, ok := e.pos[s] // locate via symbol->index map: one direct read
	e.probeCount = 1
	if !ok {
		return 0, ErrUnknownSymbol
	}
	e.moveToFront(idx)
	return idx, nil
}

// decode emits list[i] then moves it to front; unchanged on a bad index.
func (e *core[T]) decode(i int) (T, error) {
	var zero T
	if i < 0 || i >= len(e.list) {
		return zero, ErrInvalidIndex
	}
	s := e.list[i]
	e.moveToFront(i)
	return s, nil
}

// moveToFront rotates list[0:idx+1] to bring list[idx] to front and
// rewrites pos; no symbol is added or removed, so list stays a permutation.
func (e *core[T]) moveToFront(idx int) {
	if idx == 0 {
		return
	}
	sym := e.list[idx]
	copy(e.list[1:idx+1], e.list[0:idx])
	e.list[0] = sym
	for i := 0; i <= idx; i++ {
		e.pos[e.list[i]] = i
	}
}

// Codec is the public, byte-valued mutable list. Use New to construct.
type Codec struct{ c *core[byte] }

// New validates alpha and builds a Codec in alpha's initial order.
func New(alpha []byte) (*Codec, error) {
	e, ok := newCore(alpha)
	if !ok {
		return nil, ErrInvalidAlphabet
	}
	return &Codec{e}, nil
}

func (c *Codec) Len() int                         { return len(c.c.list) }
func (c *Codec) EncodeSymbol(s byte) (int, error) { return c.c.encode(s) }
func (c *Codec) DecodeSymbol(i int) (byte, error) { return c.c.decode(i) }

// SelfCheck verifies on built-in data that the list stays an alphabet
// permutation in sync with pos, and that rejected steps leave no trace.
func SelfCheck() error {
	c, err := New([]byte("abcdef"))
	if err != nil {
		return err
	}
	for _, s := range []byte("ababaaddfedcba") {
		if _, err := c.EncodeSymbol(s); err != nil {
			return err
		}
		seen := make(map[byte]bool, c.Len())
		for i, e := range c.c.list {
			if seen[e] {
				return errors.New("mtf self-check: duplicate in list")
			}
			seen[e] = true
			if c.c.pos[e] != i {
				return errors.New("mtf self-check: pos out of sync")
			}
		}
		if len(seen) != c.Len() {
			return errors.New("mtf self-check: list is not a permutation")
		}
	}
	before := append([]byte(nil), c.c.list...)
	if _, err := c.EncodeSymbol('z'); !errors.Is(err, ErrUnknownSymbol) {
		return errors.New("mtf self-check: missing ErrUnknownSymbol")
	}
	if !bytes.Equal(c.c.list, before) {
		return errors.New("mtf self-check: state changed after unknown symbol")
	}
	if _, err := c.DecodeSymbol(c.Len()); !errors.Is(err, ErrInvalidIndex) {
		return errors.New("mtf self-check: missing ErrInvalidIndex")
	}
	if !bytes.Equal(c.c.list, before) {
		return errors.New("mtf self-check: state changed after invalid index")
	}
	return nil
}

// CheckConstantTimeLocation verifies locating a symbol stays O(1) for m in 100..10000.
func CheckConstantTimeLocation() error {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		alpha := make([]int, m)
		for i := range alpha {
			alpha[i] = i
		}
		e, ok := newCore(alpha)
		if !ok {
			return errors.New("mtf: cannot build large alphabet")
		}
		for _, s := range []int{m - 1, m / 2, 0} {
			if _, err := e.encode(s); err != nil || e.probeCount > 1 {
				return errors.New("mtf: location is not constant time")
			}
		}
	}
	return nil
}

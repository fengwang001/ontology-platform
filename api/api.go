// Package api is the public entry point for blocked matrix multiplication.
// It validates inputs, distinguishes three failure classes with sentinel
// errors, and keeps no state that a rejected call could touch.
package api

import (
	"errors"
	"sync/atomic"

	"ontology/blk"
	"ontology/mul"
)

var (
	// ErrEmpty is returned when n < 1.
	ErrEmpty = errors.New("api: empty matrix: n must be >= 1")
	// ErrDim is returned when an input does not hold exactly n*n values.
	ErrDim = errors.New("api: dimension mismatch: a and b must each have length n*n")
	// ErrBlockSize is returned when the block size is outside [1,n].
	ErrBlockSize = errors.New("api: block size out of range: need 1 <= b <= n")

	errMismatch = errors.New("api: blocked result differs from naive")
)

// Engine multiplies matrices with a fixed block size. The zero value is
// not ready; use New.
type Engine struct {
	b       int
	okCalls atomic.Int64 // successful Mul calls only; rejections never touch it
}

// New builds an Engine using block size b. An invalid b is stored as-is
// and surfaced per call by Mul (validity also depends on n).
func New(b int) *Engine { return &Engine{b: b} }

// Mul returns C = a*b. It validates everything before doing any work, so
// a rejected call changes no state (including internal counters) and the
// engine stays usable. Inputs are only read, so concurrent calls sharing
// a and b are safe.
func (e *Engine) Mul(a, b []float64, n int) ([]float64, error) {
	if n < 1 {
		return nil, ErrEmpty
	}
	if len(a) != n*n || len(b) != n*n {
		return nil, ErrDim
	}
	if e.b < 1 || e.b > n {
		return nil, ErrBlockSize
	}
	c := mul.Blocked(a, b, n, e.b)
	e.okCalls.Add(1)
	return c, nil
}

// SelfCheck runs the built-in verification of the four invariants:
// blocked/naive bit-equality (tails included), partition coverage via
// blk, determinism across repeated calls, and counters untouched by
// rejections. It returns nil when all hold.
func (e *Engine) SelfCheck() error {
	if err := blk.SelfCheck(); err != nil {
		return err
	}
	for _, c := range []struct{ n, b int }{
		{1, 1}, {5, 2}, {6, 3}, {7, 4}, {13, 5},
	} {
		eng := New(c.b)
		a := make([]float64, c.n*c.n)
		bb := make([]float64, c.n*c.n)
		for i := 0; i < c.n; i++ {
			for j := 0; j < c.n; j++ {
				a[i*c.n+j] = float64(i*c.n+j+1) - float64(c.n*c.n)/2
				bb[i*c.n+j] = float64(i - j) // negatives and zeros
			}
		}
		got, err := eng.Mul(a, bb, c.n)
		if err != nil {
			return err
		}
		want := mul.Naive(a, bb, c.n)
		for i := range got {
			if got[i] != want[i] {
				return errMismatch
			}
		}
		again, err := eng.Mul(a, bb, c.n)
		if err != nil || !equalBits(got, again) {
			return errors.New("api: nondeterministic result")
		}
	}
	// A fresh engine with b=0 is guaranteed to reject any n; verify the
	// three distinct sentinels and that rejection leaves its counter at 0.
	bad := New(0)
	if _, err := bad.Mul(nil, nil, 0); !errors.Is(err, ErrEmpty) {
		return errors.New("api: empty matrix not rejected")
	}
	if _, err := bad.Mul([]float64{1}, []float64{1}, 2); !errors.Is(err, ErrDim) {
		return errors.New("api: dimension mismatch not rejected")
	}
	if _, err := bad.Mul(make([]float64, 4), make([]float64, 4), 2); !errors.Is(err, ErrBlockSize) {
		return errors.New("api: bad block size not rejected")
	}
	if bad.okCalls.Load() != 0 {
		return errors.New("api: rejection mutated internal counter")
	}
	return nil
}

// equalBits compares two float64 slices bit-for-bit.
func equalBits(x, y []float64) bool {
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

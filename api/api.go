// Package api is the public entry point for the seqlock-protected array. It
// depends only on sw (which depends on seq); the dependency direction never
// reverses.
package api

import (
	"errors"

	"ontology/seq"
	"ontology/sw"
)

// ErrInvalidSize is the fourth, distinct sentinel error.
var ErrInvalidSize = errors.New("api: New requires a positive length")

// Array is a fixed-length int64 array guarded by a seqlock.
type Array struct {
	core *seq.Core
	wr   *sw.Writer
}

// New creates an n-element array. n must be positive.
func New(n int) (*Array, error) {
	if n <= 0 {
		return nil, ErrInvalidSize
	}
	c := seq.NewCore(n)
	return &Array{core: c, wr: sw.NewWriter(c)}, nil
}

// Read returns a consistent snapshot (a copy that equals one complete
// Update's terminal value). It never blocks or waits on a writer.
func (a *Array) Read() ([]int64, error) {
	return a.core.Snapshot(), nil
}

// Update runs f with exclusive writer access; f mutates the backing slice in
// place, element by element.
func (a *Array) Update(f func(*[]int64)) error {
	return a.wr.Update(f)
}

// Seq returns the current version counter (for tests and SelfCheck).
func (a *Array) Seq() uint64 { return a.core.Seq() }

// SelfCheck exercises the invariants in NOTES.md section 2, including the
// three error classes that are only reachable through this public surface.
func (a *Array) SelfCheck() error {
	if err := a.core.SelfCheck(); err != nil {
		return err
	}
	if _, err := New(0); !errors.Is(err, ErrInvalidSize) {
		return errors.New("api: New(0) must return ErrInvalidSize")
	}
	if _, err := New(-3); !errors.Is(err, ErrInvalidSize) {
		return errors.New("api: New(-3) must return ErrInvalidSize")
	}
	if err := a.Update(nil); !errors.Is(err, sw.ErrNilFunc) {
		return errors.New("api: Update(nil) must return ErrNilFunc")
	}
	var reentrant error
	if err := a.Update(func(p *[]int64) {
		reentrant = a.Update(func(q *[]int64) { (*q)[0] = 1 })
	}); err != nil || !errors.Is(reentrant, sw.ErrReentrant) {
		return errors.New("api: re-entrant Update must return ErrReentrant")
	}
	before := a.Seq()
	arr, _ := a.Read()
	if err := a.Update(func(p *[]int64) { panic("x") }); !errors.Is(err, sw.ErrPanicInUpdate) {
		return errors.New("api: panicking Update must return wrapped ErrPanicInUpdate")
	}
	if a.Seq() != before+2 || a.Seq()&1 != 0 {
		return errors.New("api: after panic seq must be even")
	}
	cur, _ := a.Read()
	for i := range cur {
		if cur[i] != arr[i] {
			return errors.New("api: rejected updates must leave the array unchanged")
		}
	}
	return nil
}

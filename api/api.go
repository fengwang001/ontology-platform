// Package api is the public façade: New/Alloc/Reset/Valid plus SelfCheck,
// which verifies the four invariants on built-in scripts. Depends on bump.
package api

import (
	"errors"
	"fmt"
	"math/rand"
	"slices"

	"ontology/arena"
	"ontology/bump"
)

// Re-exported decidable sentinel errors; the four are pairwise distinct.
var (
	ErrInvalidSize    = bump.ErrInvalidSize
	ErrInvalidAlign   = bump.ErrInvalidAlign
	ErrArenaTooSmall  = bump.ErrArenaTooSmall
	ErrArenaExhausted = bump.ErrArenaExhausted
)

// Allocator is the thread-safe public allocator handle.
type Allocator struct{ b *bump.Allocator }

// New constructs an allocator. Bad arguments fail before any state exists.
func New(arenaSize, align, maxArenas int) (*Allocator, error) {
	x, err := bump.New(arenaSize, align, maxArenas)
	if err != nil {
		return nil, err
	}
	return &Allocator{b: x}, nil
}

func (a *Allocator) Alloc(n int) (off, gen int, err error) { return a.b.Alloc(n) }
func (a *Allocator) Reset()                                { a.b.Reset() }
func (a *Allocator) Valid(off, gen int) bool               { return a.b.Valid(off, gen) }

type placed struct{ off, sz, gen int }

// SelfCheck verifies the four invariants on the section-3 script and random
// interleavings: conservation/non-overlap, naive agreement, alignment and
// generation-scoped dangling detection, and no-trace rejections.
func (a *Allocator) SelfCheck() error {
	if err := checkCanonical(); err != nil {
		return err
	}
	rng := rand.New(rand.NewSource(1))
	for t := 0; t < 64; t++ {
		al := []int{1, 2, 4, 8, 16, 32}[rng.Intn(6)]
		ops := make([]bump.Op, 80)
		for i := range ops {
			if rng.Intn(8) == 0 {
				ops[i] = bump.Op{Reset: true}
			} else {
				ops[i] = bump.Op{N: -1 + rng.Intn(al*8+2)}
			}
		}
		if err := checkScript(al*(1+rng.Intn(8)), al, 1+rng.Intn(6), ops); err != nil {
			return err
		}
	}
	return nil
}

func checkCanonical() error {
	want := []bump.Outcome{
		{Off: 0, Gen: 0}, {Off: 16, Gen: 0}, {Off: 32, Gen: 0}, {Off: 40, Gen: 0},
		{Off: 0, Gen: 1}, {Off: 8, Gen: 1},
	}
	ops := []bump.Op{{N: 5}, {N: 10}, {N: 5}, {N: 3}, {Reset: true}, {N: 5}, {N: 5}}
	x, err := bump.New(16, 8, 3)
	if err != nil {
		return err
	}
	ri := 0
	for _, op := range ops {
		if op.Reset {
			x.Reset()
			continue
		}
		off, gen, e := x.Alloc(op.N)
		if e != nil || off != want[ri].Off || gen != want[ri].Gen {
			return fmt.Errorf("canonical step %d: got (%d,%d,%v), want %+v", ri, off, gen, e, want[ri])
		}
		ri++
	}
	if x.Valid(0, 0) || !x.Valid(0, 1) {
		return errors.New("canonical: Valid(0,0)=false and Valid(0,1)=true required after Reset")
	}
	return checkScript(16, 8, 3, append(ops, bump.Op{N: 5}, bump.Op{N: 0}))
}

func checkScript(size, al, max int, ops []bump.Op) error {
	x, err := bump.New(size, al, max)
	if err != nil {
		return err
	}
	var got []bump.Outcome
	var live []placed // placements of the current generation
	var seen []placed // every successful placement, for dangling checks
	for _, op := range ops {
		if op.Reset {
			x.Reset()
			_, gen, _ := x.Snapshot()
			for _, p := range seen {
				if p.gen < gen && x.Valid(p.off, p.gen) {
					return errors.New("pre-Reset offset reported valid after Reset")
				}
			}
			live = nil
			continue
		}
		_, _, bs0 := x.Snapshot()
		off, gen, e := x.Alloc(op.N)
		_, _, bs1 := x.Snapshot()
		got = append(got, bump.Outcome{Off: off, Gen: gen, Err: e})
		if e != nil {
			if !errors.Is(e, bump.ErrInvalidSize) && !errors.Is(e, bump.ErrArenaExhausted) {
				return fmt.Errorf("unexpected error %w", e)
			}
			if !slices.Equal(bs0, bs1) {
				return errors.New("rejected Alloc changed bumps or opened-arena count")
			}
			continue
		}
		sz := arena.AlignUp(op.N, al)
		if off%al != 0 || off/size != (off+sz-1)/size {
			return fmt.Errorf("off %d misaligned or straddles an arena", off)
		}
		p := placed{off, sz, gen}
		for _, q := range live {
			if off < q.off+q.sz && q.off < off+sz {
				return fmt.Errorf("overlap [%d,%d) vs [%d,%d)", q.off, q.off+q.sz, off, off+sz)
			}
		}
		live, seen = append(live, p), append(seen, p)
	}
	ref := bump.NaiveSim(size, al, max, ops)
	if len(ref) != len(got) {
		return fmt.Errorf("naive ref length %d != got %d", len(ref), len(got))
	}
	for i := range ref {
		if ref[i].Off != got[i].Off || ref[i].Gen != got[i].Gen ||
			!errors.Is(got[i].Err, ref[i].Err) {
			return fmt.Errorf("step %d: naive %+v != got %+v", i, ref[i], got[i])
		}
	}
	return nil
}

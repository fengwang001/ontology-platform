// Package api is the public facade of the object pool. It depends only on the
// lower opool/blk layers; the dependency direction never reverses.
package api

import (
	"errors"
	"fmt"
	"math/rand"

	"ontology/blk"
	"ontology/opool"
)

// Sentinel errors are the same distinct, inspectable values at every layer.
var (
	ErrInvalidMaxIdle   = opool.ErrInvalidMaxIdle
	ErrDuplicateRelease = opool.ErrDuplicateRelease
	ErrUnknownBlock     = opool.ErrUnknownBlock
)

// Block is an opaque, equal-sized pooled object. Callers cannot construct a
// live one; identity comparison with == is supported.
type Block struct{ b *blk.Block }

// String identifies a block (e.g. b3) for diagnostics and demos.
func (b Block) String() string {
	if b.b == nil {
		return "b<nil>"
	}
	return fmt.Sprintf("b%d", b.b.ID())
}

// Pool is the public pool handle.
type Pool struct{ p *opool.Pool }

// New creates a pool allowing at most maxIdle idle blocks.
func New(maxIdle int) (*Pool, error) {
	q, err := opool.New(maxIdle)
	if err != nil {
		return nil, err
	}
	return &Pool{p: q}, nil
}

// Acquire returns the LIFO-top idle block or a freshly allocated one.
func (p *Pool) Acquire() (Block, error) {
	b, err := p.p.Acquire()
	if err != nil {
		return Block{}, err
	}
	return Block{b: b}, nil
}

// Release returns a block; it is reclaimed when the idle list is already full.
func (p *Pool) Release(b Block) error {
	if b.b == nil {
		return ErrUnknownBlock
	}
	return p.p.Release(b.b)
}

// Idle returns the current number of idle blocks (<= maxIdle).
func (p *Pool) Idle() int { return p.p.Idle() }

// Total returns live blocks (in-use + idle).
func (p *Pool) Total() int { return p.p.Total() }

const (
	stInUse = 1
	stIdle  = 2
)

// SelfCheck replays built-in operation sequences against a naive LIFO
// reference model and verifies the four invariants: conservation/uniqueness,
// agreement with the naive model (including LIFO order), full-pool eviction
// (reclaimed blocks never return, Idle never exceeds maxIdle), and no-trace
// rejections. It neither inspects nor mutates the receiver's own state.
func (p *Pool) SelfCheck() error {
	for _, maxIdle := range []int{1, 2, 3, 7} {
		q, err := New(maxIdle)
		if err != nil {
			return err
		}
		rng := rand.New(rand.NewSource(int64(maxIdle)*1000 + 7))
		status := map[Block]int{}
		var idle []Block   // naive idle stack
		inUse := []Block{} // blocks currently in-use (release candidates)
		dead := map[Block]bool{}
		for step := 0; step < 2000; step++ {
			if rng.Intn(2) == 0 {
				got, err := q.Acquire()
				if err != nil {
					return fmt.Errorf("SelfCheck: acquire error: %w", err)
				}
				if dead[got] {
					return fmt.Errorf("SelfCheck: reclaimed block %s returned", got)
				}
				var want Block
				if n := len(idle); n > 0 { // naive LIFO pop
					want, idle = idle[n-1], idle[:n-1]
				}
				if want != (Block{}) && got != want {
					return fmt.Errorf("SelfCheck: LIFO mismatch: got %s want %s", got, want)
				}
				status[got] = stInUse
				inUse = append(inUse, got)
			} else if len(inUse) > 0 {
				i := rng.Intn(len(inUse))
				b := inUse[i]
				inUse = append(inUse[:i], inUse[i+1:]...)
				if err := q.Release(b); err != nil {
					return fmt.Errorf("SelfCheck: release error: %w", err)
				}
				if len(idle) >= maxIdle { // naive eviction
					delete(status, b)
					dead[b] = true
				} else {
					status[b] = stIdle
					idle = append(idle, b)
				}
			} else { // nothing in-use: probe a rejection (unknown block)
				if err := q.Release(Block{}); !errors.Is(err, ErrUnknownBlock) {
					return fmt.Errorf("SelfCheck: unknown release want ErrUnknownBlock, got %v", err)
				}
			}
			// Every now and then reject a duplicate / unknown release, and
			// verify accounting is untouched afterwards.
			if rng.Intn(20) == 0 && len(idle) > 0 {
				beforeI, beforeT := q.Idle(), q.Total()
				errDup := q.Release(idle[len(idle)-1])
				if !errors.Is(errDup, ErrDuplicateRelease) || q.Idle() != beforeI || q.Total() != beforeT {
					return fmt.Errorf("SelfCheck: duplicate release left a trace")
				}
			}
			wantIdle := len(idle)
			wantTotal := len(status)
			if q.Idle() != wantIdle || q.Total() != wantTotal || q.Idle() > maxIdle {
				return fmt.Errorf("SelfCheck: accounting mismatch at step %d", step)
			}
		}
	}
	return nil
}

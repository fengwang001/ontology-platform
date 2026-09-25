// Package opool is the fixed-size-block reuse pool. It depends only on blk.
package opool

import (
	"errors"
	"sync"

	"ontology/blk"
)

// Sentinel errors: every rejection has a distinct, inspectable cause.
var (
	// ErrInvalidMaxIdle: New was called with maxIdle < 1.
	ErrInvalidMaxIdle = errors.New("opool: maxIdle must be >= 1")
	// ErrDuplicateRelease: Release of a block that is already idle.
	ErrDuplicateRelease = errors.New("opool: block is already idle (duplicate release)")
	// ErrUnknownBlock: Release of a block never acquired or already reclaimed.
	ErrUnknownBlock = errors.New("opool: unknown or reclaimed block")
)

// Pool is a LIFO reuse pool of equal-sized blocks bounded by maxIdle.
type Pool struct {
	mu      sync.Mutex
	maxIdle int
	nextID  uint64
	live    map[uint64]*blk.Block // in-use + idle; reclaimed blocks deleted
	idle    blk.Stack
	// checked is the number of idle-stack records inspected by the most
	// recent Acquire/Release. Unexported: never reachable via the public API.
	checked int
}

// New creates a pool allowing at least 1 and at most maxIdle idle blocks.
// An illegal argument is rejected before any state comes into existence.
func New(maxIdle int) (*Pool, error) {
	if maxIdle < 1 {
		return nil, ErrInvalidMaxIdle
	}
	return &Pool{maxIdle: maxIdle, live: make(map[uint64]*blk.Block)}, nil
}

// Acquire pops the LIFO top when an idle block exists (one record inspected),
// otherwise allocates a fresh block (zero idle records inspected).
func (p *Pool) Acquire() (*blk.Block, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if b, ok := p.idle.Pop(); ok {
		p.checked = 1
		if err := b.ToInUse(); err != nil {
			// Impossible given pool invariants; leave no trace if it happens.
			p.idle.Push(b)
			return nil, err
		}
		return b, nil
	}
	p.checked = 0
	b := blk.New(p.nextID)
	p.nextID++
	p.live[b.ID()] = b
	return b, nil
}

// Release pushes an in-use block when the idle list is below maxIdle; when it
// is already full the block is reclaimed (terminal, Total decremented) and
// never handed out again. Every validation precedes any state change.
func (p *Pool) Release(b *blk.Block) error {
	if b == nil {
		return ErrUnknownBlock
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	got, ok := p.live[b.ID()]
	if !ok || got != b {
		p.checked = 0
		return ErrUnknownBlock // never acquired, or already reclaimed
	}
	if b.State() == blk.Idle {
		p.checked = 0
		return ErrDuplicateRelease
	}
	p.checked = 0 // length/capacity checks read no idle-stack records
	if blk.AtCapacity(p.idle.Len(), p.maxIdle) {
		if err := b.Reclaim(); err != nil {
			return err
		}
		delete(p.live, b.ID())
		return nil
	}
	if err := b.ToIdle(); err != nil {
		return err
	}
	p.idle.Push(b)
	return nil
}

// Idle returns the current number of idle blocks (always <= maxIdle).
func (p *Pool) Idle() int {
	p.mu.Lock()
	n := p.idle.Len()
	p.mu.Unlock()
	return n
}

// Total returns the number of live blocks (in-use + idle).
func (p *Pool) Total() int {
	p.mu.Lock()
	n := len(p.live)
	p.mu.Unlock()
	return n
}

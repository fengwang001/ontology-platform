// Package blk implements the block lifecycle state machine, the LIFO idle
// stack and the full-pool eviction predicate. It depends on no other package.
package blk

import "errors"

// State is the unique lifecycle state of a block.
type State uint8

const (
	// InUse: acquired and not yet released.
	InUse State = iota + 1
	// Idle: sitting on the idle stack, available for LIFO reuse.
	Idle
	// Reclaimed: evicted from a full pool; terminal, never handed out again.
	Reclaimed
)

// Sentinel errors for illegal state transitions; distinct and inspectable.
var (
	ErrNotInUse = errors.New("blk: block is not in-use")
	ErrNotIdle  = errors.New("blk: block is not idle")
)

// Block is an equal-sized opaque object tracked by its unique id and state.
// A Block is never copied: the pool holds and hands out *Block pointers.
type Block struct {
	id    uint64
	state State
}

// New creates a block with the given id, born InUse.
func New(id uint64) *Block { return &Block{id: id, state: InUse} }

// ID returns the block's unique id.
func (b *Block) ID() uint64 { return b.id }

// State returns the current state.
func (b *Block) State() State { return b.state }

// ToIdle moves InUse -> Idle (Release of an in-use block).
func (b *Block) ToIdle() error {
	if b.state != InUse {
		return ErrNotInUse
	}
	b.state = Idle
	return nil
}

// ToInUse moves Idle -> InUse (LIFO pop on Acquire).
func (b *Block) ToInUse() error {
	if b.state != Idle {
		return ErrNotIdle
	}
	b.state = InUse
	return nil
}

// Reclaim moves InUse -> Reclaimed; the terminal state must never leave.
func (b *Block) Reclaim() error {
	if b.state != InUse {
		return ErrNotInUse
	}
	b.state = Reclaimed
	return nil
}

// Stack is a non-thread-safe LIFO stack of idle blocks; the embedding pool
// guards it with its own mutex. Push/Pop touch only the top node, so each
// operation examines at most one idle record: O(1), never a full-list scan.
type Stack struct {
	top *node
	n   int
}

type node struct {
	b     *Block
	below *node
}

// Push puts b on top without reading any existing record.
func (s *Stack) Push(b *Block) {
	s.top = &node{b: b, below: s.top}
	s.n++
}

// Pop removes and returns the top block, examining exactly that one record.
func (s *Stack) Pop() (*Block, bool) {
	t := s.top
	if t == nil {
		return nil, false
	}
	s.top = t.below
	s.n--
	return t.b, true
}

// Len returns the number of idle blocks.
func (s *Stack) Len() int { return s.n }

// AtCapacity reports whether an idle list of idleLen blocks already reaches
// the maxIdle limit, in which case a released block must be reclaimed rather
// than pushed. A pure predicate over two ints: examines no idle records.
func AtCapacity(idleLen, maxIdle int) bool { return idleLen >= maxIdle }

// Package seq holds the seqlock version counter and the protected,
// fixed-length int64 array. It depends on no other package.
package seq

import (
	"errors"
	"fmt"
	"sync/atomic"
)

// Core is a seqlock-protected array. seq is even when the array is stable and
// odd while a write is in progress.
type Core struct {
	seq    atomic.Uint64
	vals   []int64 // committed elements; never swapped wholesale
	writes uint64  // unexported: element writes counted by the last Commit
}

// NewCore creates an n-element Core, all zero, with seq == 0.
func NewCore(n int) *Core {
	return &Core{vals: make([]int64, n)}
}

// Len returns the fixed array length.
func (c *Core) Len() int { return len(c.vals) }

// Seq returns the current version counter (read only; never advanced here).
func (c *Core) Seq() uint64 { return c.seq.Load() }

// Begin opens the write phase. The caller must hold writer exclusivity: seq
// starts even and becomes odd. It returns a private shadow slice that the
// writer mutates element by element with ordinary assignments.
func (c *Core) Begin() []int64 {
	shadow := make([]int64, len(c.vals))
	for i := range c.vals {
		shadow[i] = atomic.LoadInt64(&c.vals[i])
	}
	c.seq.Add(1) // enter the odd phase before any element changes
	return shadow
}

// Commit publishes the shadow element by element under the odd seq (so a
// concurrent reader can observe a mixed new/old array and must retry), counts
// the elements whose value actually changed, then restores an even seq.
func (c *Core) Commit(shadow []int64) {
	n := 0
	for i := range c.vals {
		v := shadow[i]
		if atomic.LoadInt64(&c.vals[i]) != v {
			atomic.StoreInt64(&c.vals[i], v) // one ordinary per-element write
			n++
		}
	}
	c.writes = uint64(n)
	c.seq.Add(1) // leave the odd phase
}

// Rollback makes seq even again after a panicked write and publishes nothing.
func (c *Core) Rollback() {
	if c.seq.Load()&1 == 1 {
		c.seq.Add(1)
	}
}

// Snapshot returns a consistent copy of the array. It records s1, retries at
// once when s1 is odd (without touching the array), copies the elements, then
// records s2 and retries unless s1 == s2. It never takes a writer lock.
func (c *Core) Snapshot() []int64 {
	for {
		s1 := c.seq.Load()
		if s1&1 == 1 {
			continue // a write is in progress
		}
		out := make([]int64, len(c.vals))
		for i := range c.vals {
			out[i] = atomic.LoadInt64(&c.vals[i])
		}
		s2 := c.seq.Load()
		if s1 == s2 { // equality is the only success criterion
			return out
		}
	}
}

// SelfCheck verifies the seqlock protocol on fresh cores and returns the
// first violation found. It reads the unexported write counter directly,
// which is only possible inside this package.
func (c *Core) SelfCheck() error {
	z := NewCore(4)
	if z.Seq() != 0 || fmt.Sprint(z.Snapshot()) != "[0 0 0 0]" {
		return errors.New("seq: initial state must be seq 0 and all-zero array")
	}
	for k := int64(1); k <= 3; k++ {
		sh := z.Begin()
		for i := range sh {
			sh[i] = k
		}
		z.Commit(sh)
		if z.Seq() != 2*uint64(k) {
			return errors.New("seq: each commit must advance seq by exactly 2")
		}
		if fmt.Sprint(z.Snapshot()) != fmt.Sprint([]int64{k, k, k, k}) {
			return errors.New("seq: snapshot must equal the last committed terminal")
		}
	}
	// Odd-phase retry: a reader entering while seq is odd must not return
	// until the write commits, and then sees the committed value.
	r := NewCore(2)
	sh := r.Begin()
	got := make(chan []int64, 1)
	go func() { got <- r.Snapshot() }()
	select {
	case <-got:
		return errors.New("seq: reader returned during the odd phase")
	default:
	}
	sh[0], sh[1] = 5, 5
	r.Commit(sh)
	if fmt.Sprint(<-got) != "[5 5]" {
		return errors.New("seq: reader must observe the committed terminal")
	}
	// O(1) writes: touching one element counts exactly one element write,
	// independent of the array length.
	for _, m := range []int{100, 1000, 10000} {
		s := NewCore(m)
		for round := int64(1); round <= 3; round++ {
			sh := s.Begin()
			sh[m/2] = round
			s.Commit(sh)
			if s.writes != 1 {
				return fmt.Errorf("seq: one-element update counted %d writes at m=%d", s.writes, m)
			}
		}
	}
	return nil
}

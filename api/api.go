// Package api is the public facade: New, operation wrappers and SelfCheck.
package api

import (
	"errors"
	"fmt"

	"ontology/rec"
)

// Ckpt and Snapshot are re-exported for callers.
type Ckpt = rec.Ckpt
type Snapshot = rec.Snapshot

// Sentinel errors, re-exported; distinguishable via errors.Is.
var (
	ErrNonPositive  = rec.ErrNonPositive
	ErrGap          = rec.ErrGap
	ErrNoCheckpoint = rec.ErrNoCheckpoint
)

// Processor is the public handle.
type Processor struct{ p *rec.Processor }

func New() *Processor { return &Processor{p: rec.New()} }

func (a *Processor) Apply(pos, delta int64) error { return a.p.Apply(pos, delta) }
func (a *Processor) Flush()                       { a.p.Flush() }
func (a *Processor) Checkpoint()                  { a.p.Checkpoint() }
func (a *Processor) Restart() (Ckpt, error)       { return a.p.Restart() }
func (a *Processor) Snapshot() Snapshot           { return a.p.Snapshot() }

// SelfCheck verifies the four invariants on built-in operation sequences.
func SelfCheck() error {
	for _, seed := range []uint64{1, 7, 42, 2026} {
		if err := checkSequence(seed); err != nil {
			return err
		}
	}
	return checkRejects()
}

// checkSequence runs a deterministic pseudo-random Apply/Flush/Checkpoint
// interleaving and asserts invariants 1 (naive-reference equality), 2
// (consistent cut) and 3 (bounds & monotonicity) throughout.
func checkSequence(seed uint64) error {
	p := New()
	var deltas []int64 // deltas[i] is the delta of position i+1
	var sumAll, lastCkpt int64
	x := seed
	rnd := func() uint64 { x = x*6364136223846793005 + 1442695040888963407; return x >> 33 }
	for i := 0; i < 300; i++ {
		switch rnd() % 3 {
		case 0:
			d := int64(rnd()%201) - 100
			if err := p.Apply(int64(len(deltas))+1, d); err != nil {
				return fmt.Errorf("selfcheck apply: %w", err)
			}
			deltas = append(deltas, d)
			sumAll += d
		case 1:
			p.Flush()
		case 2:
			p.Checkpoint()
		}
		s := p.Snapshot()
		if s.Flushed > s.Applied { // invariant 3, bounds
			return fmt.Errorf("selfcheck seed %d: flushed>applied", seed)
		}
		if s.Ckpt.Valid {
			if s.Ckpt.Pos > s.Flushed || s.Ckpt.Pos < lastCkpt { // invariant 3
				return fmt.Errorf("selfcheck seed %d: ckpt pos out of order", seed)
			}
			lastCkpt = s.Ckpt.Pos
			var sum int64 // invariant 2: ckpt.total == sum of pos<=ckpt.pos
			for j := int64(0); j < s.Ckpt.Pos; j++ {
				sum += deltas[j]
			}
			if sum != s.Ckpt.Total {
				return fmt.Errorf("selfcheck seed %d: ckpt total mismatch", seed)
			}
		}
	}
	p.Flush()
	p.Checkpoint()
	c, err := p.Restart() // invariant 1: restart + replay == naive total
	if err != nil {
		return fmt.Errorf("selfcheck seed %d: %w", seed, err)
	}
	for pos := c.Pos + 1; pos <= int64(len(deltas)); pos++ {
		if err := p.Apply(pos, deltas[pos-1]); err != nil {
			return fmt.Errorf("selfcheck replay: %w", err)
		}
	}
	if got := p.Snapshot().Total; got != sumAll {
		return fmt.Errorf("selfcheck seed %d: replay total %d != naive %d", seed, got, sumAll)
	}
	return nil
}

// checkRejects asserts invariant 4: rejected operations change nothing.
func checkRejects() error {
	p := New()
	if err := p.Apply(1, 5); err != nil {
		return err
	}
	p.Flush()
	p.Checkpoint()
	before := p.Snapshot()
	for _, c := range []struct {
		op   func() error
		want error
	}{
		{func() error { return p.Apply(0, 1) }, ErrNonPositive},
		{func() error { return p.Apply(-3, 1) }, ErrNonPositive},
		{func() error { return p.Apply(3, 1) }, ErrGap},
	} {
		if err := c.op(); !errors.Is(err, c.want) {
			return fmt.Errorf("selfcheck: want %v, got %v", c.want, err)
		}
		if p.Snapshot() != before {
			return errors.New("selfcheck: rejected op mutated state")
		}
	}
	if _, err := New().Restart(); !errors.Is(err, ErrNoCheckpoint) {
		return fmt.Errorf("selfcheck: want %v, got %v", ErrNoCheckpoint, err)
	}
	return nil
}

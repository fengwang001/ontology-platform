// Package api is the public facade for checkpoint-consistent log truncation.
// It depends on ontology/trunc -> ontology/log (one direction); memory only.
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/log"
	"ontology/trunc"
)

// The three failure kinds are distinct sentinels, decidable with errors.Is.
var (
	ErrCheckpointInvalid        = log.ErrCheckpointInvalid
	ErrTruncateBeyondCheckpoint = trunc.ErrTruncateBeyondCheckpoint
	ErrRecoverOverDeletion      = trunc.ErrRecoverOverDeletion
)

// System is safe for concurrent use.
type System struct {
	mu sync.RWMutex
	lg *log.Log
	tr *trunc.Truncator
}

func New() *System {
	lg := log.New()
	return &System{lg: lg, tr: trunc.New(lg)}
}

func (s *System) Append(payload string) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lg.Append(payload)
}
func (s *System) Checkpoint(off uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lg.Checkpoint(off)
}

// Truncate is two-phase (marker then physical delete) and atomic for readers:
// it runs under the write lock, so Read never sees marker K with [0,K) half gone.
func (s *System) Truncate(k uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tr.Truncate(k)
}

func (s *System) Recover(marker, first uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tr.Recover(marker, first)
}

func (s *System) Read(from uint64) []log.Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lg.Read(from)
}

func (s *System) First() uint64 { s.mu.RLock(); defer s.mu.RUnlock(); return s.lg.First() }

func (s *System) CP() (uint64, bool) { s.mu.RLock(); defer s.mu.RUnlock(); return s.lg.CP() }

func (s *System) Marker() (uint64, bool) { s.mu.RLock(); defer s.mu.RUnlock(); return s.tr.Marker() }

// SelfCheck replays the specification's eight-step sequence on an isolated
// system and verifies all four invariants. Nil only if every step matches.
func (s *System) SelfCheck() error {
	c := New()
	var all []string // naive reference: every appended payload, in offset order
	var cut uint64   // largest completed truncation K
	cpVal := func() int64 {
		if v, ok := c.lg.CP(); ok {
			return int64(v)
		}
		return -1
	}
	type row struct{ cp, tm, f, lo, hi int64 } // cp -1=none; lo/hi -1=empty
	want := []row{
		{-1, 0, 0, 0, 0}, {-1, 0, 0, 0, 1}, {-1, 0, 0, 0, 2},
		{2, 0, 0, 0, 2}, {2, 2, 2, 2, 2}, {2, 2, 2, 2, 3},
		{3, 2, 2, 2, 3}, {3, 3, 3, 3, 3},
	}
	ops := []func() error{
		func() error { _, e := c.Append("a"); return e },
		func() error { _, e := c.Append("b"); return e },
		func() error { _, e := c.Append("c"); return e },
		func() error { return c.Checkpoint(2) },
		// Invariant 1: at cp=2, T(3) is refused (would drop offset 2).
		func() error {
			if !errors.Is(c.Truncate(3), ErrTruncateBeyondCheckpoint) {
				return fmt.Errorf("T(3) at cp=2 not refused")
			}
			return c.Truncate(2)
		},
		func() error { _, e := c.Append("d"); return e },
		func() error { return c.Checkpoint(3) },
		func() error { return c.Recover(3, 2) }, // step 8: T(3)+CR -> converge f=3
	}
	for i, op := range ops {
		if err := op(); err != nil {
			return fmt.Errorf("selfcheck step %d: %w", i+1, err)
		}
		switch i {
		case 0, 1, 2:
			all = append(all, []string{"a", "b", "c"}[i])
		case 4:
			cut = 2
		case 5:
			all = append(all, "d")
		case 7:
			cut = 3
		}
		tm, _ := c.tr.Marker()
		es := c.Read(0)
		// Invariant 2: live entries == naive append-only log minus prefix.
		if len(es) != len(all)-int(cut) {
			return fmt.Errorf("step %d: naive length %d != %d", i+1, len(es), len(all)-int(cut))
		}
		for j, e := range es {
			if e.Payload != all[int(cut)+j] {
				return fmt.Errorf("step %d: naive content mismatch at %d", i+1, j)
			}
		}
		got := row{cpVal(), int64(tm), int64(c.lg.First()), -1, -1}
		if len(es) > 0 {
			got.lo, got.hi = int64(es[0].Offset), int64(es[len(es)-1].Offset)
		}
		if got != want[i] { // invariant 3: tm==f once completed/recovered
			return fmt.Errorf("step %d: got %+v want %+v", i+1, got, want[i])
		}
	}
	// Recovery clean + corruption branches, and invariant 4 (no trace).
	if err := c.Recover(3, 3); err != nil {
		return err
	}
	before := c.First()
	if !errors.Is(c.Recover(3, 4), ErrRecoverOverDeletion) {
		return fmt.Errorf("f>tm not reported as corruption")
	}
	if c.First() != before {
		return fmt.Errorf("over-delete recovery left a trace")
	}
	return nil
}

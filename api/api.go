// Package api is the outward face of the preemptive event processor.
package api

import (
	"errors"
	"fmt"
	"reflect"
	"sync"

	"ontology/sched"
)

// Sentinel errors, distinct and stable for errors.Is.
var (
	ErrBadPrio = sched.ErrBadPrio
	ErrDupID   = sched.ErrDupID
	ErrIdle    = sched.ErrIdle
)

// Processor serializes access to the scheduling state machine.
type Processor struct {
	mu sync.Mutex
	sc *sched.Sched
}

// New returns an idle processor.
func New() *Processor { return &Processor{sc: sched.New()} }

func (p *Processor) Submit(id, prio int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sc.Submit(id, prio)
}

// Process advances one event and returns its ID and priority.
func (p *Processor) Process() (int, int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sc.Process()
}

func (p *Processor) Done() []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sc.Done()
}

// Snapshot returns a deep copy of the full machine state.
func (p *Processor) Snapshot() sched.State {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sc.Snapshot()
}

// SelfCheck verifies the four invariants on built-in sequences using
// fresh instances only, so it is safe to call concurrently.
func SelfCheck() error {
	// Invariant 1: the eight-op script matches the hand-derived reference.
	sc1 := sched.New()
	for _, op := range [][2]int{{1, 1}, {2, 1}, {0, -1}, {3, 3}, {4, 5}, {0, -1}, {0, -1}, {0, -1}} {
		if op[1] < 0 {
			if _, _, err := sc1.Process(); err != nil {
				return fmt.Errorf("selfcheck: script process: %w", err)
			}
		} else if err := sc1.Submit(op[0], op[1]); err != nil {
			return fmt.Errorf("selfcheck: script submit: %w", err)
		}
	}
	if got := sc1.Done(); !reflect.DeepEqual(got, []int{1, 4, 3, 2}) {
		return fmt.Errorf("selfcheck: script done=%v, want [1 4 3 2]", got)
	}
	// Invariants 2,3: random interleaving — no dup, no loss, same-prio FIFO.
	sc := sched.New()
	processed := make(map[int]bool)
	lastID := make(map[int]int)
	id := 0
	rng := uint32(1)
	next := func(n uint32) uint32 { rng = rng*1664525 + 1013904223; return rng % n }
	for i := 0; i < 600; i++ {
		if next(100) < 55 {
			if err := sc.Submit(id, int(next(6))); err != nil {
				return fmt.Errorf("selfcheck: submit(%d): %w", id, err)
			}
			id++
			continue
		}
		gotID, gotPrio, err := sc.Process()
		if len(processed) == id {
			if !errors.Is(err, ErrIdle) {
				return fmt.Errorf("selfcheck: idle process: %v", err)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("selfcheck: process: %w", err)
		}
		if processed[gotID] || gotID >= id {
			return fmt.Errorf("selfcheck: id %d processed twice or never submitted", gotID)
		}
		if gotID < lastID[gotPrio] {
			return fmt.Errorf("selfcheck: FIFO violated at prio %d", gotPrio)
		}
		processed[gotID] = true
		lastID[gotPrio] = gotID
	}
	for len(processed) < id { // drain: every submitted event exactly once
		gotID, _, err := sc.Process()
		if err != nil || processed[gotID] {
			return fmt.Errorf("selfcheck: drain: id %d, err %v", gotID, err)
		}
		processed[gotID] = true
	}
	return checkFaults()
}

// checkFaults verifies distinct sentinel errors and no-trace rejection.
func checkFaults() error {
	if ErrBadPrio == ErrDupID || ErrDupID == ErrIdle || ErrBadPrio == ErrIdle {
		return errors.New("selfcheck: sentinel errors are not distinct")
	}
	sc := sched.New()
	if err := sc.Submit(1, 0); err != nil {
		return fmt.Errorf("selfcheck: seed submit: %w", err)
	}
	before := sc.Snapshot()
	if err := sc.Submit(2, -1); !errors.Is(err, ErrBadPrio) {
		return fmt.Errorf("selfcheck: negative prio: %v", err)
	}
	if err := sc.Submit(1, 0); !errors.Is(err, ErrDupID) {
		return fmt.Errorf("selfcheck: duplicate id: %v", err)
	}
	if !reflect.DeepEqual(before, sc.Snapshot()) {
		return errors.New("selfcheck: rejected submit changed state")
	}
	if _, _, err := sc.Process(); err != nil {
		return fmt.Errorf("selfcheck: drain: %w", err)
	}
	idleSnap := sc.Snapshot()
	if _, _, err := sc.Process(); !errors.Is(err, ErrIdle) {
		return fmt.Errorf("selfcheck: empty process: %v", err)
	}
	if !reflect.DeepEqual(idleSnap, sc.Snapshot()) {
		return errors.New("selfcheck: idle process changed state")
	}
	if err := sc.Submit(2, 1); err != nil { // still usable after rejections
		return fmt.Errorf("selfcheck: submit after rejections: %w", err)
	}
	_, _, err := sc.Process()
	return err
}

// Package api is the public face of the checkpointed accumulator.
package api

import (
	"errors"
	"fmt"
	"math/rand"

	"ontology/acc"
)

// Distinguishable sentinel errors. A rejected Apply changes nothing.
var (
	ErrEmptyKey       = errors.New("api: empty key")
	ErrNegativeOffset = errors.New("api: negative offset")
	ErrInvalidLimit   = errors.New("api: maxPending must be positive")
	ErrPendingFull    = acc.ErrPendingFull
)

// A is the accumulator handle. All methods are safe for concurrent use.
type A struct{ core *acc.Acc }

// New returns an accumulator capped at maxPending pending records.
func New(maxPending int) (*A, error) {
	if maxPending <= 0 {
		return nil, ErrInvalidLimit
	}
	return &A{core: acc.New(maxPending)}, nil
}

// Apply buffers one record; persisted or inflight duplicates are
// idempotent no-ops. Bad input is rejected and leaves no trace.
func (a *A) Apply(key string, offset, delta int64) error {
	if key == "" {
		return ErrEmptyKey
	}
	if offset < 0 {
		return ErrNegativeOffset
	}
	return a.core.Apply(key, offset, delta)
}

func (a *A) Commit()            { a.core.Commit() }            // fold contiguous prefix
func (a *A) Checkpoint() int64  { return a.core.Checkpoint() } // committed cp
func (a *A) Sum(k string) int64 { return a.core.Sum(k) }       // committed sum
func (a *A) Restore()           { a.core.Restore() }           // crash: drop pending
func (a *A) Pending() []int64   { return a.core.Pending() }    // inflight offsets

// ref is a brute-force reference: on commit it rescans from 0.
type ref struct {
	seen map[int64]acc.Record // committed + inflight
	done []acc.Record         // committed log
	cpN  int64
}

func newRef() *ref { return &ref{seen: map[int64]acc.Record{}, cpN: -1} }
func (r *ref) apply(k string, o, d int64) {
	if _, ok := r.seen[o]; !ok {
		r.seen[o] = acc.Record{Key: k, Delta: d}
	}
}
func (r *ref) commit() {
	t := int64(-1)
	for ; ; t++ {
		if _, ok := r.seen[t+1]; !ok {
			break
		}
	}
	for o := r.cpN + 1; o <= t; o++ {
		r.done = append(r.done, r.seen[o])
	}
	r.cpN = t
}
func (r *ref) restore() {
	for o := range r.seen {
		if o > r.cpN {
			delete(r.seen, o)
		}
	}
}
func (r *ref) sum(key string) (s int64) {
	for _, rec := range r.done {
		if rec.Key == key {
			s += rec.Delta
		}
	}
	return
}

// SelfCheck verifies the four invariants on fresh instances.
func (a *A) SelfCheck() error {
	keys := []string{"a", "b", "c"}
	rng := rand.New(rand.NewSource(518))
	for _, tc := range []struct{ ops, maxOff, maxP int }{{300, 40, 64}, {700, 90, 128}} {
		sys, _ := New(tc.maxP)
		rf := newRef()
		for i := 0; i < tc.ops; i++ {
			switch rng.Intn(4) {
			case 0, 1:
				k, o := keys[rng.Intn(3)], int64(rng.Intn(tc.maxOff))
				d := int64(rng.Intn(201) - 100)
				if err := sys.Apply(k, o, d); err != nil {
					return fmt.Errorf("selfcheck apply: %w", err)
				}
				rf.apply(k, o, d)
			case 2:
				sys.Commit()
				rf.commit()
			case 3:
				sys.Restore()
				rf.restore()
			}
			if sys.Checkpoint() != rf.cpN {
				return fmt.Errorf("selfcheck cp: got %d want %d", sys.Checkpoint(), rf.cpN)
			}
			for _, k := range keys {
				if sys.Sum(k) != rf.sum(k) {
					return fmt.Errorf("selfcheck sum[%s]: got %d want %d", k, sys.Sum(k), rf.sum(k))
				}
			}
		}
	}
	return checkRejections()
}

// checkRejections verifies failures are distinguishable and leave no
// trace, and that the instance keeps working afterwards.
func checkRejections() error {
	sys, _ := New(1)
	_ = sys.Apply("k", 0, 7) // cannot fail: valid input, empty buffer
	state := func() string { return fmt.Sprint(sys.Checkpoint(), sys.Sum("k"), sys.Pending()) }
	before := state()
	if err := sys.Apply("", 1, 1); !errors.Is(err, ErrEmptyKey) || state() != before {
		return fmt.Errorf("selfcheck empty-key: err=%v", err)
	}
	if err := sys.Apply("k", -1, 1); !errors.Is(err, ErrNegativeOffset) || state() != before {
		return fmt.Errorf("selfcheck neg-offset: err=%v", err)
	}
	if err := sys.Apply("k", 9, 1); !errors.Is(err, ErrPendingFull) || state() != before {
		return fmt.Errorf("selfcheck pending-full: err=%v", err)
	}
	if ErrEmptyKey == ErrNegativeOffset || ErrEmptyKey == ErrPendingFull || ErrNegativeOffset == ErrPendingFull {
		return errors.New("selfcheck: sentinels not distinct")
	}
	sys.Commit()
	if sys.Checkpoint() != 0 || sys.Sum("k") != 7 {
		return errors.New("selfcheck: instance unusable after rejections")
	}
	return nil
}

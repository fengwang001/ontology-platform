// Package api is the public, concurrency-safe face of the retransmission
// timer: one timer on the earliest pending segment, exponential backoff.
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/internal/rtx"
)

// The four failures are distinct, decidable sentinel errors.
var (
	ErrInvalidRTO    = rtx.ErrInvalidRTO
	ErrSeqOutOfOrder = rtx.ErrSeqOutOfOrder
	ErrNegativeAck   = rtx.ErrNegativeAck
	ErrClockRollback = rtx.ErrClockRollback
)

// Timer is safe for concurrent use.
type Timer struct {
	mu sync.Mutex
	e  *rtx.Engine
}

// New builds a Timer; baseRTO must be positive.
func New(baseRTO int64) (*Timer, error) {
	e, err := rtx.New(baseRTO)
	if err != nil {
		return nil, err
	}
	return &Timer{e: e}, nil
}
func (t *Timer) Send(seq, now int64) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.e.Send(seq, now)
}
func (t *Timer) Ack(a, now int64) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.e.Ack(a, now)
}

// Tick performs one timeout check; a rejected clock never reports a timeout.
func (t *Timer) Tick(now int64) (bool, int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	to, seq, err := t.e.Tick(now)
	if err != nil {
		return false, 0
	}
	return to, seq
}
func (t *Timer) RTO() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.e.State().RTO()
}
func (t *Timer) HasDeadline() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.e.State().HasDeadline()
}
func (t *Timer) Deadline() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.e.State().Deadline()
}
func (t *Timer) Backoff() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.e.State().Backoff()
}
func (t *Timer) Base() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.e.State().Base()
}

// SelfCheck replays the eight built-in operations and the four rejection
// cases on a fresh instance, verifying invariants I1..I4 via the API.
// Rows are {rto, deadline, armed, timeout, retrans, backoff}.
func (t *Timer) SelfCheck() error {
	u, err := New(10)
	if err != nil {
		return err
	}
	b := func(v bool) int64 {
		if v {
			return 1
		}
		return 0
	}
	want := [][6]int64{
		{10, 10, 1, 0, 0, 0}, {20, 30, 1, 1, 0, 1},
		{20, 30, 1, 0, 0, 1}, {40, 70, 1, 1, 0, 2},
		{10, 0, 0, 0, 0, 0}, {10, 60, 1, 0, 0, 0},
		{20, 80, 1, 1, 1, 1}, {10, 0, 0, 0, 0, 0},
	}
	ops := [][3]int64{
		{'s', 0, 0}, {'t', 0, 10}, {'t', 0, 20}, {'t', 0, 30},
		{'a', 1, 40}, {'s', 1, 50}, {'t', 0, 60}, {'a', 2, 60},
	}
	for i, o := range ops {
		var to bool
		var seq int64
		switch o[0] {
		case 's':
			if err := u.Send(o[1], o[2]); err != nil {
				return err
			}
		case 'a':
			if err := u.Ack(o[1], o[2]); err != nil {
				return err
			}
		case 't':
			to, seq = u.Tick(o[2])
		}
		got := [6]int64{u.RTO(), u.Deadline(), b(u.HasDeadline()), b(to), seq, int64(u.Backoff())}
		if got != want[i] {
			return fmt.Errorf("selfcheck step %d: %v != %v", i+1, got, want[i])
		}
	}
	v, _ := New(10)
	if err := v.Send(0, 0); err != nil {
		return err
	}
	snap := func() [5]int64 {
		return [5]int64{v.Base(), v.RTO(), v.Deadline(), int64(v.Backoff()), b(v.HasDeadline())}
	}
	for _, c := range []struct {
		err error
		fn  func() error
	}{
		{ErrSeqOutOfOrder, func() error { return v.Send(0, 0) }},
		{ErrNegativeAck, func() error { return v.Ack(-1, 0) }},
		{ErrClockRollback, func() error { return v.Send(1, -1) }},
	} {
		before := snap()
		if !errors.Is(c.fn(), c.err) || snap() != before {
			return fmt.Errorf("selfcheck rejection %v changed state", c.err)
		}
	}
	if _, err := New(0); !errors.Is(err, ErrInvalidRTO) {
		return fmt.Errorf("selfcheck baseRTO: %v", err)
	}
	return v.Send(1, 5)
}

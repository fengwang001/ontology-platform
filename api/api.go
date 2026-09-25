// Package api is the public face of the time-partitioned
// materializer with rolling retention.
package api

import (
	"errors"
	"fmt"

	"ontology/tbucket"
	"ontology/tpart"
)

// Distinguishable sentinel errors for every rejectable fault.
var (
	ErrNonPositiveSize = errors.New("api: size must be positive")
	ErrNonPositiveR    = errors.New("api: R must be positive")
	ErrEmptyKey        = errors.New("api: event key must be non-empty")
)

// Event is a single arriving record.
type Event struct {
	TS  int64
	Key string
}

// Mat is the materialized-view handle. Safe for concurrent use.
type Mat struct {
	size, r int64
	p       *tpart.Part
}

// New validates parameters before any state exists.
func New(size, r int64) (*Mat, error) {
	if size <= 0 {
		return nil, ErrNonPositiveSize
	}
	if r <= 0 {
		return nil, ErrNonPositiveR
	}
	return &Mat{size: size, r: r, p: tpart.New(size, r)}, nil
}

// Feed applies a batch atomically: every key is validated first, so a
// rejected batch changes nothing (counts, cur, dropped all unchanged).
func (m *Mat) Feed(evs []Event) error {
	for _, e := range evs {
		if e.Key == "" {
			return ErrEmptyKey
		}
	}
	for _, e := range evs {
		m.p.Add(e.TS, e.Key)
	}
	return nil
}

// View returns the current counts as key -> bucket -> count.
func (m *Mat) View() map[string]map[int64]int64 { return m.p.Snapshot() }

// Dropped returns the total number of dropped events.
func (m *Mat) Dropped() int64 { return m.p.Dropped() }

// SelfCheck verifies the four invariants on built-in sequences. It
// only uses fresh internal instances, never the receiver's state.
func SelfCheck() error {
	if tbucket.FloorDiv(-12, 10) != -2 || tbucket.FloorDiv(-1, 10) != -1 {
		return fmt.Errorf("invariant 2: floorDiv wrong on negatives")
	}
	m, err := New(10, 3)
	if err != nil {
		return err
	}
	seq := []int64{5, -12, 0, 10, -1, 20, 9, 30}
	for _, ts := range seq {
		if err := m.Feed([]Event{{TS: ts, Key: "k"}}); err != nil {
			return err
		}
	}
	v := m.View()["k"]
	if len(v) != 3 || v[1] != 1 || v[2] != 1 || v[3] != 1 || m.Dropped() != 5 {
		return fmt.Errorf("invariants 1&3: rolling retention mismatch")
	}
	before, dBefore := m.View(), m.Dropped()
	if err := m.Feed([]Event{{TS: 40, Key: ""}}); !errors.Is(err, ErrEmptyKey) {
		return fmt.Errorf("invariant 4: empty key not rejected")
	}
	if m.Dropped() != dBefore || len(m.View()["k"]) != len(before["k"]) {
		return fmt.Errorf("invariant 4: rejected batch left a trace")
	}
	return nil
}

// Package api is the public facade for hinted handoff.
package api

import (
	"errors"
	"fmt"

	"ontology/hh"
)

// Re-export the four distinct, decision-ready sentinel errors.
var (
	ErrConfig       = hh.ErrConfig
	ErrVersion      = hh.ErrVersion
	ErrKey          = hh.ErrKey
	ErrHintOverflow = hh.ErrHintOverflow
)

// Entry is a snapshot of one replica's current value for a key.
type Entry struct {
	Value string
	Ver   int64
	Set   bool // false means the replica has never applied this key
}

// API is safe for concurrent use by multiple goroutines.
type API struct {
	c *hh.Cluster
}

// New builds n initially-online replicas with per-down-replica hint cap.
func New(n, maxHints int) (*API, error) {
	c, err := hh.New(n, maxHints)
	if err != nil {
		return nil, err
	}
	return &API{c: c}, nil
}

// Write fans a strict newer-than update out to online replicas; down replicas
// receive a hint. The whole write fails (and leaves no trace) on overflow.
func (a *API) Write(key, value string, ver int64) error {
	return a.c.Write(key, value, ver)
}

// Down takes a replica offline.
func (a *API) Down(r int) error { return a.c.Down(r) }

// Up brings a replica back and replays its hints, returning counts.
func (a *API) Up(r int) (applied, skipped int, err error) {
	return a.c.Up(r)
}

// Get returns a snapshot of every replica's current entry for key.
func (a *API) Get(key string) []Entry {
	s := a.c.Snapshot(key)
	out := make([]Entry, len(s))
	for i := range s {
		out[i] = Entry(s[i])
	}
	return out
}

// SelfCheck replays a built-in operation sequence (including the specification's
// eight-step scenario) and verifies all four invariants, returning nil on pass.
func (a *API) SelfCheck() error {
	fresh, err := hh.New(3, 3)
	if err != nil {
		return err
	}
	down := func(i int) { _ = fresh.Down(i) }
	up := func(i, ap, sk int) error {
		gap, gsk, e := fresh.Up(i)
		if e != nil || gap != ap || gsk != sk {
			return fmt.Errorf("Up(%d)=(%d,%d,%v), want (%d,%d)", i, gap, gsk, e, ap, sk)
		}
		return nil
	}
	write := func(v string, x int64, want error) error {
		if e := fresh.Write("k", v, x); !errors.Is(e, want) {
			return fmt.Errorf("Write(%s,%d)=%v, want %v", v, x, e, want)
		}
		return nil
	}
	val := func(i int, want string, x int64) error {
		e := fresh.Snapshot("k")[i]
		if e.Value != want || e.Ver != x {
			return fmt.Errorf("R%d=(%s,%d), want (%s,%d)", i, e.Value, e.Ver, want, x)
		}
		return nil
	}
	down(1) // R1 down before step 1
	if e := write("a", 5, nil); e != nil {
		return e
	}
	if e := val(0, "a", 5); e != nil {
		return e
	}
	if e := write("b", 7, nil); e != nil {
		return e
	}
	if e := write("c", 6, nil); e != nil {
		return e
	} // online skip stale; hint still appended
	if e := val(0, "b", 7); e != nil {
		return e
	}
	if e := write("d", 8, hh.ErrHintOverflow); e != nil { // step 4: full
		return e
	}
	if e := val(0, "b", 7); e != nil {
		return e // no trace: R0 unchanged
	}
	if e := up(1, 2, 1); e != nil { // step 5: a5,b7 applied; c6 skipped
		return e
	}
	if e := val(1, "b", 7); e != nil { // late stale version skipped
		return e
	}
	if e := up(1, 0, 0); e != nil { // idempotent replay converges
		return e
	}
	down(1)
	if e := write("e", 9, nil); e != nil { // step 6
		return e
	}
	if e := write("f", 9, nil); e != nil { // step 7: tie skipped online, hinted anyway
		return e
	}
	if e := val(0, "e", 9); e != nil {
		return e
	}
	if e := up(1, 1, 1); e != nil { // step 8: f9 tie skipped
		return e
	}
	for i := 0; i < 3; i++ {
		if e := val(i, "e", 9); e != nil {
			return fmt.Errorf("invariant 1: %w", e)
		}
	}
	return nil
}

// Package api is the public face of the heartbeat liveness detector.
// It depends on sweep (which depends on hb); nothing depends back.
package api

import (
	"errors"
	"fmt"

	"ontology/hb"
	"ontology/sweep"
)

var (
	ErrEmptyID      = sweep.ErrEmptyID
	ErrNegativeTime = hb.ErrNegativeTime
	ErrClockBack    = hb.ErrClockBack
	ErrStale        = hb.ErrStale
)

const (
	Absent = hb.Absent
	Active = hb.Active
	Idle   = hb.Idle
	Dead   = hb.Dead
)

// ViewEntry is one row of View.
type ViewEntry = sweep.ViewEntry

// Detector is the public liveness detector. Not copied after New.
type Detector struct{ m *sweep.Manager }

// New returns an empty detector.
func New() *Detector { return &Detector{m: sweep.New()} }

// Heartbeat records ts for id; stale or invalid input leaves state intact.
func (d *Detector) Heartbeat(id string, ts int64) error { return d.m.Heartbeat(id, ts) }

// Status returns the stream's state at now.
func (d *Detector) Status(id string, now int64) (hb.State, error) { return d.m.Status(id, now) }

// Sweep lists all dead stream ids, ascending.
func (d *Detector) Sweep(now int64) ([]string, error) { return d.m.Sweep(now) }

// View snapshots all known streams, sorted by id.
func (d *Detector) View() []ViewEntry { return d.m.View() }

// SelfCheck replays built-in event sequences on a scratch detector and
// returns the first violation of the four spec invariants, or nil.
func SelfCheck() error {
	checks := []struct {
		name string
		run  func(*Detector) error
	}{
		{"monotonic", checkMonotonic}, {"naive-reference", checkNaive},
		{"transitions", checkTransitions}, {"rejection-leaves-no-trace", checkNoTrace},
	}
	for _, c := range checks {
		if err := c.run(New()); err != nil {
			return fmt.Errorf("selfcheck %s: %w", c.name, err)
		}
	}
	return nil
}

// Invariant 1: lastHb never decreases; stale heartbeats change nothing.
func checkMonotonic(d *Detector) error {
	if err := d.Heartbeat("s", 40); err != nil {
		return err
	}
	if err := d.Heartbeat("s", 20); !errors.Is(err, ErrStale) {
		return fmt.Errorf("stale not reported: %v", err)
	}
	st, err := d.Status("s", 45) // age 5 iff lastHb still 40
	if err != nil || st != Active {
		return fmt.Errorf("lastHb moved: state=%v err=%v", st, err)
	}
	return nil
}

// Invariant 2: state equals the naive age-threshold reference everywhere.
func checkNaive(d *Detector) error {
	if err := d.Heartbeat("s", 7); err != nil {
		return err
	}
	for now := int64(7); now <= 60; now++ {
		got, err := d.Status("s", now)
		if err != nil {
			return err
		}
		if want := hb.Classify(now - 7); got != want {
			return fmt.Errorf("now=%d: got %v want %v", now, got, want)
		}
	}
	return nil
}

// Invariant 3: active->idle->dead by age; a new heartbeat resets to active.
func checkTransitions(d *Detector) error {
	type q struct {
		now  int64
		want hb.State
	}
	seq := []q{{10, Active}, {11, Idle}, {30, Idle}, {31, Dead}}
	if err := d.Heartbeat("s", 0); err != nil {
		return err
	}
	for _, q := range seq {
		if got, _ := d.Status("s", q.now); got != q.want {
			return fmt.Errorf("now=%d: got %v want %v", q.now, got, q.want)
		}
	}
	if err := d.Heartbeat("s", 40); err != nil { // recovery after dead
		return err
	}
	if got, _ := d.Status("s", 40); got != Active {
		return fmt.Errorf("recovery: got %v want active", got)
	}
	return nil
}

// Invariant 4: rejections (empty id, negative ts/now, rollback) leave no trace.
func checkNoTrace(d *Detector) error {
	if err := d.Heartbeat("s", 10); err != nil {
		return err
	}
	for _, err := range []error{
		d.Heartbeat("", 5), d.Heartbeat("s", -1),
	} {
		if err == nil {
			return errors.New("bad heartbeat accepted")
		}
	}
	if _, err := d.Status("", 10); !errors.Is(err, ErrEmptyID) {
		return fmt.Errorf("empty id: %v", err)
	}
	if _, err := d.Status("s", -1); !errors.Is(err, ErrNegativeTime) {
		return fmt.Errorf("negative now: %v", err)
	}
	if _, err := d.Status("s", 9); !errors.Is(err, ErrClockBack) {
		return fmt.Errorf("clock rollback: %v", err)
	}
	if _, err := d.Sweep(-1); !errors.Is(err, ErrNegativeTime) {
		return fmt.Errorf("negative sweep: %v", err)
	}
	if got, _ := d.Status("s", 15); got != Active { // age 5, unchanged
		return fmt.Errorf("state changed after rejections: %v", got)
	}
	return d.Heartbeat("s", 20) // still usable
}

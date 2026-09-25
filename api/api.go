// Package api is the public face of the writer-preference readers-writer lock.
package api

import (
	"errors"
	"fmt"
	"strings"

	"ontology/lock"
)

// Re-exported sentinels so callers can errors.Is against them.
var (
	ErrEmptyID       = lock.ErrEmptyID
	ErrNotHeld       = lock.ErrNotHeld
	ErrDoubleRelease = lock.ErrDoubleRelease
)

// API is a writer-preference readers-writer lock, safe for concurrent
// use. Acquire returns true when granted, false when queued.
type API struct{ l *lock.Lock }

func New() *API { return &API{l: lock.New()} }

func (a *API) AcquireRead(id string) (bool, error)  { return a.l.AcquireRead(id) }
func (a *API) AcquireWrite(id string) (bool, error) { return a.l.AcquireWrite(id) }
func (a *API) ReleaseRead(id string) error          { return a.l.ReleaseRead(id) }
func (a *API) ReleaseWrite(id string) error         { return a.l.ReleaseWrite(id) }
func (a *API) Readers() []string                    { return a.l.Readers() }
func (a *API) Writer() string                       { return a.l.Writer() }

// traceStep is one step of the eight-step sequence derived in NOTES.md.
// op: ar=AcquireRead aw=AcquireWrite rr=ReleaseRead rw=ReleaseWrite.
type traceStep struct {
	op, id  string
	readers string // expected held readers, comma-joined, "" = none
	writer  string
	granted bool // expected result for acquires
}

var trace = []traceStep{
	{"ar", "R1", "R1", "", true},
	{"ar", "R2", "R1,R2", "", true},
	{"aw", "W1", "R1,R2", "", false},
	{"ar", "R3", "R1,R2", "", false},
	{"rr", "R1", "R2", "", false},
	{"rr", "R2", "", "W1", false},
	{"ar", "R4", "", "W1", false},
	{"rw", "W1", "R3,R4", "", false},
}

// checkTrace runs the NOTES.md eight-step sequence: invariants 1-3.
func checkTrace() error {
	a := New()
	for i, s := range trace {
		g := false
		switch s.op {
		case "ar":
			g, _ = a.AcquireRead(s.id)
		case "aw":
			g, _ = a.AcquireWrite(s.id)
		case "rr":
			_ = a.ReleaseRead(s.id)
		case "rw":
			_ = a.ReleaseWrite(s.id)
		}
		got := strings.Join(a.Readers(), ",")
		if got != s.readers || a.Writer() != s.writer || (s.op[0] == 'a' && g != s.granted) {
			return fmt.Errorf("step %d (%s %s): readers=%q writer=%q granted=%v, want %+v",
				i+1, s.op, s.id, got, a.Writer(), g, s)
		}
	}
	return nil
}

// checkWriterPref: a waiting writer blocks new readers (invariant 2).
func checkWriterPref() error {
	f := New()
	if _, err := f.AcquireRead("R1"); err != nil {
		return err
	}
	if ok, _ := f.AcquireWrite("W1"); ok {
		return errors.New("selfcheck: writer entered over held reader")
	}
	if ok, _ := f.AcquireRead("R2"); ok {
		return errors.New("selfcheck: reader entered over waiting writer")
	}
	return nil
}

// checkFaults: the three rejections are distinct, leave no trace, and
// the lock stays usable afterwards (invariant 4).
func checkFaults() error {
	f := New()
	if _, err := f.AcquireRead("R1"); err != nil {
		return err
	}
	before := fmt.Sprint(f.Readers(), f.Writer())
	rejects := []struct {
		run  func() error
		want error
	}{
		{func() error { _, e := f.AcquireRead(""); return e }, ErrEmptyID},
		{func() error { return f.ReleaseRead("R9") }, ErrNotHeld},
		{func() error { return f.ReleaseWrite("R1") }, ErrNotHeld},
	}
	for _, rj := range rejects {
		if err := rj.run(); !errors.Is(err, rj.want) {
			return fmt.Errorf("selfcheck: got %v, want %v", err, rj.want)
		}
	}
	if fmt.Sprint(f.Readers(), f.Writer()) != before {
		return errors.New("selfcheck: rejected operation changed state")
	}
	if err := f.ReleaseRead("R1"); err != nil {
		return err
	}
	if err := f.ReleaseRead("R1"); !errors.Is(err, ErrDoubleRelease) {
		return errors.New("selfcheck: double release not rejected")
	}
	if ok, _ := f.AcquireRead("R3"); !ok {
		return errors.New("selfcheck: lock unusable after rejections")
	}
	return nil
}

// SelfCheck verifies the four invariants on built-in request/release
// sequences; it returns nil when all hold, else the first violation.
func (a *API) SelfCheck() error {
	for _, check := range []func() error{checkTrace, checkWriterPref, checkFaults} {
		if err := check(); err != nil {
			return err
		}
	}
	return nil
}

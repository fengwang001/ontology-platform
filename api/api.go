// Package api is the public facade over exp/wlog.
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/exp"
	"ontology/wlog"
)

// ErrTooManyExports is returned when unfinished exports exceed maxExports.
var ErrTooManyExports = errors.New("api: too many concurrent exports")

// Exporter couples the log with bounded concurrent export sessions.
type Exporter struct {
	log        *wlog.Log
	mu         sync.Mutex
	active     int
	maxExports int
}

// New creates an Exporter allowing up to maxExports unfinished exports.
func New(maxExports int) *Exporter {
	return &Exporter{log: wlog.New(), maxExports: maxExports}
}

// Write appends to the log; rejected writes consume no Seq.
func (e *Exporter) Write(key string, val int) (int, error) {
	return e.log.Write(key, val)
}

// StartExport opens a session with S = current MaxSeq, enforcing the bound.
func (e *Exporter) StartExport() (*exp.Session, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.active >= e.maxExports {
		return nil, ErrTooManyExports
	}
	e.active++
	s := exp.NewSession(e.log, func() {
		e.mu.Lock()
		e.active--
		e.mu.Unlock()
	})
	if err := s.Start(e.log.MaxSeq()); err != nil {
		e.active--
		return nil, err
	}
	return s, nil
}

// Next emits the next entry of the session.
func (e *Exporter) Next(s *exp.Session) (wlog.Entry, bool, error) {
	return s.Next()
}

// Finish closes the session and reports the two segment ranges.
func (e *Exporter) Finish(s *exp.Session) (snapHi, incLo, incHi int, err error) {
	return s.Finish()
}

// drain collects all entries a session can currently emit.
func drain(e *Exporter, s *exp.Session) []int {
	var out []int
	for {
		ent, ok, err := e.Next(s)
		if err != nil || !ok {
			return out
		}
		out = append(out, ent.Seq)
	}
}

// SelfCheck runs built-in interleavings verifying the four invariants.
func SelfCheck() error {
	for seed := 0; seed < 20; seed++ {
		ex := New(2)
		s, err := ex.StartExport()
		if err != nil {
			return err
		}
		var emitted []int
		for step := 0; step < 40; step++ { // deterministic interleaving
			if (step+seed)%3 == 0 {
				if _, err := ex.Write(fmt.Sprintf("k%d-%d", seed, step), step); err != nil {
					return err
				}
			} else {
				emitted = append(emitted, drain(ex, s)...)
			}
		}
		emitted = append(emitted, drain(ex, s)...)
		hi, lo, incHi, err := ex.Finish(s)
		if err != nil {
			return err
		}
		for i, seq := range emitted { // contiguous, ascending, naive-equal
			if seq != i+1 {
				return fmt.Errorf("selfcheck: emitted %v not 1..n", emitted)
			}
			ent, _ := ex.log.Entry(seq)
			if ent.Seq != seq {
				return errors.New("selfcheck: mismatch with naive read")
			}
		}
		if hi != 0 || lo != 1 || incHi != len(emitted) {
			return fmt.Errorf("selfcheck: bad ranges [%d][%d,%d]", hi, lo, incHi)
		}
		if _, err := ex.Write("", 0); !errors.Is(err, wlog.ErrEmptyKey) {
			return errors.New("selfcheck: empty key accepted")
		}
	}
	return nil
}

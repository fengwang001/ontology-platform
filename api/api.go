// Package api is the external facade over txlog and rc.
package api

import (
	"errors"
	"fmt"

	"ontology/rc"
	"ontology/txlog"
)

// API is the in-process transactional-partition service.
type API struct {
	log *txlog.Log
}

// New returns an empty service.
func New() *API { return &API{log: txlog.New()} }

// AppendData appends Data(pid, val) and returns its offset.
func (a *API) AppendData(pid int, val string) (int, error) {
	return a.log.AppendData(pid, val)
}

// AppendCommit appends Commit(pid) and returns its offset.
func (a *API) AppendCommit(pid int) (int, error) {
	return a.log.AppendCommit(pid)
}

// AppendAbort appends Abort(pid) and returns its offset.
func (a *API) AppendAbort(pid int) (int, error) {
	return a.log.AppendAbort(pid)
}

// AdvanceHW advances the high-water mark.
func (a *API) AdvanceHW(h int) error { return a.log.AdvanceHW(h) }

// HW returns the current high-water mark.
func (a *API) HW() int { return a.log.HW() }

// LSO returns the last stable offset.
func (a *API) LSO() int { return a.log.LSO() }

// Fetch performs one read_committed fetch starting at from.
func (a *API) Fetch(from int) (vals []string, next int, err error) {
	return rc.Read(a.log, from)
}

var fail = errors.New("api: self-check failed")

// SelfCheck replays the built-in sequence from the task brief and verifies
// the four invariants: streaming==batch, LSO legality/monotonicity,
// committed-only visibility, and no trace after rejected operations.
func (a *API) SelfCheck() error {
	type op struct {
		kind byte
		pid  int
		val  string
	}
	ops := []op{
		{'D', 1, "a"}, {'D', 2, "b"}, {'D', 1, "c"}, {'C', 1, ""},
		{'D', 3, "d"}, {'D', 2, "e"}, {'A', 2, ""}, {'D', 3, "f"},
		{'D', 1, "g"}, {'C', 3, ""}, {'A', 1, ""},
	}
	for _, o := range ops {
		switch o.kind {
		case 'D':
			if _, err := a.AppendData(o.pid, o.val); err != nil {
				return err
			}
		case 'C':
			if _, err := a.AppendCommit(o.pid); err != nil {
				return err
			}
		default:
			if _, err := a.AppendAbort(o.pid); err != nil {
				return err
			}
		}
	}
	hws := []int{2, 4, 6, 7, 9, 10, 11}
	wantLSO := []int{0, 1, 1, 4, 4, 8, 11}
	wantOut := [][]string{nil, {"a"}, nil, {"c"}, nil, {"d", "f"}, nil}
	var got, stream []string
	from, prevLSO := 0, 0
	for i, h := range hws {
		if err := a.AdvanceHW(h); err != nil {
			return err
		}
		lso := a.LSO() // invariant 2: legal and non-decreasing
		if lso > a.HW() || lso != wantLSO[i] || lso < prevLSO {
			return fmt.Errorf("%w: LSO step %d = %d", fail, i, lso)
		}
		prevLSO = lso
		got, from, _ = a.Fetch(from) // invariant 1/3: incremental fetch
		if !equal(got, wantOut[i]) {
			return fmt.Errorf("%w: fetch step %d = %v", fail, i, got)
		}
		stream = append(stream, got...)
	}
	batch, _, err := a.Fetch(0) // one-shot batch scan over final [0, LSO)
	if err != nil || !equal(stream, batch) {
		return fmt.Errorf("%w: stream %v != batch %v", fail, stream, batch)
	}
	want := []string{"a", "c", "d", "f"}
	if !equal(batch, want) { // pid1 committed-then-aborted judged per txn
		return fmt.Errorf("%w: batch = %v", fail, batch)
	}
	// Invariant 4: the four distinct sentinel errors change no state.
	sentinels := []error{txlog.ErrInvalidRecord, txlog.ErrNoActiveTransaction,
		txlog.ErrHWOutOfRange, rc.ErrInvalidFrom}
	hw, lso, end := a.HW(), a.LSO(), 11
	before, _, _ := a.Fetch(0)
	tries := []func() error{
		func() error { _, e := a.AppendData(0, "x"); return e },
		func() error { _, e := a.AppendCommit(99); return e },
		func() error { return a.AdvanceHW(-1) },
		func() error { _, _, e := a.Fetch(lso + 1); return e },
	}
	for i, tr := range tries {
		e := tr()
		after, _, _ := a.Fetch(0)
		if !errors.Is(e, sentinels[i]) || a.HW() != hw || a.LSO() != lso ||
			end != 11 || !equal(after, before) {
			return fmt.Errorf("%w: rejection %d left a trace: %v", fail, i, e)
		}
	}
	return nil
}

func equal(x, y []string) bool {
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

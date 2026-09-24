// Package rapply: replica table, per-event judgement, atomic batch apply.
package rapply

import (
	"errors"
	"sync"

	"ontology/rimg"
)

var (
	ErrSeqGap      = errors.New("rapply: seq not equal to previous+1")
	ErrTooManyRows = errors.New("rapply: replica row count exceeds maxRows")
)

type Replica struct {
	mu        sync.Mutex
	rows      map[int64]rimg.Row
	conflicts []rimg.Conflict
	lastSeq   int64
	maxRows   int
	reads     int // unexported rows-read counter; never exposed by any method
}

func New(initial map[int64]rimg.Row, maxRows int) *Replica {
	return &Replica{rows: clone(initial), maxRows: maxRows}
}

func (r *Replica) LastSeq() int64 { r.mu.Lock(); defer r.mu.Unlock(); return r.lastSeq }

// State returns one consistent committed triple: rows, conflict log, lastSeq.
func (r *Replica) State() (map[int64]rimg.Row, []rimg.Conflict, int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]rimg.Conflict, len(r.conflicts))
	copy(out, r.conflicts)
	return clone(r.rows), out, r.lastSeq
}

func (r *Replica) Snapshot() map[int64]rimg.Row { rows, _, _ := r.State(); return rows }
func (r *Replica) Conflicts() []rimg.Conflict   { _, out, _ := r.State(); return out }

// Apply processes one batch atomically; a conflict consumes a seq number.
func (r *Replica) Apply(evs []rimg.Event) ([]rimg.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]rimg.Result, len(evs))
	if len(evs) == 0 {
		return out, nil
	}
	expected := r.lastSeq + 1
	for _, e := range evs { // reject whole batch before touching working state
		if err := rimg.ValidEvent(e); err != nil {
			return nil, err
		}
		if e.Seq != expected {
			return nil, ErrSeqGap
		}
		expected++
	}
	work := clone(r.rows) // stage on a clone; commit only on full success
	staged := []rimg.Conflict{}
	for i, e := range evs {
		r.reads = 0
		res, conf := r.judge(work, e)
		res.Conflict = conf // 0 for applied events
		out[i] = res
		if !res.Applied {
			staged = append(staged, rimg.Conflict{Seq: e.Seq, PK: e.PK, Type: conf})
			continue
		}
		if e.Kind == rimg.Delete {
			delete(work, e.PK)
		} else {
			work[e.PK] = dupRow(e.After) // Insert/Update: blind write After
		}
		if len(work) > r.maxRows {
			return nil, ErrTooManyRows // clone and staged log discarded
		}
	}
	r.rows = work
	r.conflicts = append(r.conflicts, staged...)
	r.lastSeq = evs[len(evs)-1].Seq
	return out, nil
}

// judge applies the first-match rule with exactly one PK lookup.
func (r *Replica) judge(work map[int64]rimg.Row, e rimg.Event) (rimg.Result, rimg.ConflictType) {
	r.reads++
	cur, ok := work[e.PK]
	res := rimg.Result{Seq: e.Seq}
	switch {
	case e.Kind == rimg.Insert && ok:
		return res, rimg.RowExists
	case e.Kind == rimg.Insert:
		res.Applied = true
	case !ok:
		return res, rimg.RowMissing
	case !rimg.RowEqual(cur, e.Before):
		return res, rimg.BeforeMismatch
	default:
		res.Applied = true
	}
	return res, 0
}

func dupRow(x rimg.Row) rimg.Row {
	c := make(rimg.Row, len(x))
	for k, v := range x {
		c[k] = v
	}
	return c
}

func clone(in map[int64]rimg.Row) map[int64]rimg.Row {
	out := make(map[int64]rimg.Row, len(in))
	for pk, row := range in {
		out[pk] = dupRow(row)
	}
	return out
}

// ReadBoundOK verifies (never exposing the counter) that judging a matching
// Update, a mismatching Delete and an existing-PK Insert reads O(1) rows.
func ReadBoundOK() bool {
	for _, m := range []int{100, 1000, 10000} {
		init := make(map[int64]rimg.Row, m)
		for i := range m {
			init[int64(i+1)] = rimg.Row{"c": "v"}
		}
		r := New(init, m+10)
		evs := []rimg.Event{
			{Seq: 1, Kind: rimg.Update, PK: 1, Before: rimg.Row{"c": "v"}, After: rimg.Row{"c": "w"}},
			{Seq: 2, Kind: rimg.Delete, PK: 2, Before: rimg.Row{"c": "x"}},
			{Seq: 3, Kind: rimg.Insert, PK: 1, After: rimg.Row{"c": "y"}},
		}
		for _, e := range evs {
			if _, err := r.Apply([]rimg.Event{e}); err != nil || r.reads > 1 {
				return false
			}
		}
	}
	return true
}

package rapply

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"

	"ontology/rimg"
)

type Outcome uint8

const (
	Applied Outcome = iota + 1
	RowExists
	RowMissing
	BeforeMismatch
)

type Result struct {
	Seq, PK int64
	Outcome Outcome
}
type Conflict struct {
	Seq, PK int64
	Type    Outcome
}

var ErrInvalidEvent, ErrSeqGap, ErrTooManyRows, errLookupGrew = errors.New("rapply: invalid event shape"), errors.New("rapply: seq not contiguous"), errors.New("rapply: replica exceeds maxRows"), errors.New("rapply: lookup reads grew with table size")

type Engine struct {
	mu             sync.RWMutex
	rows           map[int64]rimg.Row
	conflicts      []Conflict
	lastSeq        int64
	maxRows, reads int // rows read judging latest event; unexported, never returned
}

func New(initial map[int64]rimg.Row, maxRows int) (*Engine, error) {
	for pk, r := range initial {
		if !rimg.ValidRow(r) {
			return nil, fmt.Errorf("%w: bad seed row pk=%d", ErrInvalidEvent, pk)
		}
	}
	e := &Engine{rows: dup(initial), maxRows: maxRows}
	if maxRows < 0 || len(e.rows) > maxRows {
		return nil, fmt.Errorf("%w: %d>%d", ErrTooManyRows, len(e.rows), maxRows)
	}
	return e, nil
}
func (e *Engine) judge(ev rimg.Event) Outcome {
	e.reads = 0
	cur, ok := e.rows[ev.PK]
	e.reads++
	if ev.Kind == rimg.Insert {
		if ok {
			return RowExists
		}
		return Applied
	}
	if !ok {
		return RowMissing
	}
	if !rimg.Equal(cur, ev.Before) {
		return BeforeMismatch
	}
	return Applied
}
func (e *Engine) step(ev rimg.Event) Outcome {
	o := e.judge(ev)
	if o != Applied {
		e.conflicts = append(e.conflicts, Conflict{ev.Seq, ev.PK, o})
		return o
	}
	if ev.Kind == rimg.Delete {
		delete(e.rows, ev.PK)
	} else {
		e.rows[ev.PK] = maps.Clone(ev.After)
	}
	return o
}

// ApplyBatch validates the whole batch then simulates on a copy and commits only on full success.
func (e *Engine) ApplyBatch(evs []rimg.Event) ([]Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i, ev := range evs {
		if !rimg.ValidEvent(ev) {
			return nil, fmt.Errorf("%w: idx=%d", ErrInvalidEvent, i)
		}
		if ev.Seq != e.lastSeq+int64(i)+1 {
			return nil, fmt.Errorf("%w: want=%d got=%d", ErrSeqGap, e.lastSeq+int64(i)+1, ev.Seq)
		}
	}
	sim := &Engine{rows: dup(e.rows), conflicts: slices.Clone(e.conflicts), maxRows: e.maxRows}
	res := make([]Result, 0, len(evs))
	for _, ev := range evs {
		o := sim.step(ev)
		if len(sim.rows) > e.maxRows {
			return nil, fmt.Errorf("%w: seq=%d", ErrTooManyRows, ev.Seq)
		}
		res = append(res, Result{ev.Seq, ev.PK, o})
	}
	e.rows, e.conflicts, e.lastSeq, e.reads = sim.rows, sim.conflicts, e.lastSeq+int64(len(evs)), sim.reads
	return res, nil
}
func (e *Engine) Snapshot() (map[int64]rimg.Row, []Conflict, int64) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return dup(e.rows), slices.Clone(e.conflicts), e.lastSeq
}
func (e *Engine) Conflicts() []Conflict {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return slices.Clone(e.conflicts)
}
func dup(m map[int64]rimg.Row) map[int64]rimg.Row {
	c := make(map[int64]rimg.Row, len(m))
	for pk, r := range m {
		c[pk] = maps.Clone(r)
	}
	return c
}

// VerifyLookupIsO1 returns pass/fail only (never the measured count).
func VerifyLookupIsO1() error {
	for _, m := range []int{100, 1000, 10000} {
		init := make(map[int64]rimg.Row, m)
		for i := range m {
			init[int64(i)+1] = rimg.Row{"c": "v"}
		}
		e, _ := New(init, m+1)
		probes := []rimg.Event{
			{Seq: 1, Kind: rimg.Update, PK: 1, Before: rimg.Row{"c": "v"}, After: rimg.Row{"c": "w"}},
			{Seq: 2, Kind: rimg.Delete, PK: 1, Before: rimg.Row{"c": "x"}},
			{Seq: 3, Kind: rimg.Insert, PK: 1, After: rimg.Row{"c": "z"}},
		}
		for _, ev := range probes {
			if _, err := e.ApplyBatch([]rimg.Event{ev}); err != nil {
				return err
			}
			if e.reads > 2 {
				return fmt.Errorf("%w: m=%d", errLookupGrew, m)
			}
		}
	}
	return nil
}

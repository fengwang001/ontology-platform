// Package dedup manages multi-partition exactly-once state on top of rec: atomic batches, snapshots, self-check.
package dedup

import (
	"errors"
	"reflect"
	"sync"

	"ontology/rec"
)

var ErrInvalidWindow = errors.New("dedup: window must be >= 1") // fourth distinguishable failure: non-positive window
var ErrNegativeOffset, ErrConflict, ErrRewound = rec.ErrNegativeOffset, rec.ErrConflict, rec.ErrRewound

type (
	Kind  int
	Event struct {
		Partition int
		Offset    int64
		Value     string
	}
	Entry  = rec.Pair // retained pair in a View snapshot
	Result struct {
		Kind Kind
		Err  error
	}
	D struct {
		mu      sync.RWMutex
		window  int
		parts   map[int]*rec.State
		applied []Event
	}
)

const Applied, Idempotent, Rejected Kind = 0, 1, 2

func mk(p int, off int64, v string) Event { return Event{Partition: p, Offset: off, Value: v} }
func res(k Kind, err error) Result        { return Result{Kind: k, Err: err} }
func New(window int) (*D, error) {
	if window < 1 {
		return nil, ErrInvalidWindow
	}
	return &D{window: window, parts: map[int]*rec.State{}}, nil
}
func (d *D) Apply(e Event) (Result, error) {
	rs, err := d.ApplyBatch([]Event{e})
	if err != nil {
		return res(Rejected, err), err
	}
	return rs[0], nil
}

// ApplyBatch is all-or-nothing: decisions run on private clones; any rejection discards them.
func (d *D) ApplyBatch(evs []Event) ([]Result, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	clones, taken := map[int]*rec.State{}, []Event{}
	rs := make([]Result, len(evs))
	for i, e := range evs {
		st, seen := clones[e.Partition]
		if !seen {
			st = rec.New(d.window)
			if x := d.parts[e.Partition]; x != nil {
				st = x.Clone()
			}
			clones[e.Partition] = st
		}
		ok, err := st.Apply(rec.Event{Offset: e.Offset, Value: e.Value})
		if err != nil {
			return nil, err // the batch never happened
		}
		rs[i] = res(Idempotent, nil)
		if ok {
			rs[i], taken = res(Applied, nil), append(taken, e)
		}
	}
	for p, st := range clones {
		d.parts[p] = st // commit phase: swap clones in
	}
	d.applied = append(d.applied, taken...)
	return rs, nil
}
func (d *D) View() map[int][]Entry {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := map[int][]Entry{}
	for p, st := range d.parts {
		out[p] = st.Snapshot()
	}
	return out
}
func naiveRebuild(w int, log []Event) map[int][]Entry {
	m := map[int]*rec.State{}
	for _, e := range log {
		if m[e.Partition] == nil {
			m[e.Partition] = rec.New(w)
		}
		_, _ = m[e.Partition].Apply(rec.Event{Offset: e.Offset, Value: e.Value})
	}
	out := map[int][]Entry{}
	for p, st := range m {
		out[p] = st.Snapshot()
	}
	return out
}
func (d *D) SelfCheck() error {
	d.mu.RLock()
	log := append([]Event(nil), d.applied...)
	got := make(map[int][]Entry, len(d.parts))
	for p, st := range d.parts { // snapshot under the held lock; View must not be re-entered here
		got[p] = st.Snapshot()
	}
	d.mu.RUnlock()
	if !reflect.DeepEqual(got, naiveRebuild(d.window, log)) {
		return errors.New("dedup: view diverges from naive rebuild")
	}
	return builtIn()
}

// builtIn replays a fixed window=4 sequence (marks a/=/C/R/N); view==naive(log) each step proves reference equality and that non-apply steps changed nothing.
func builtIn() error {
	d, _ := New(4)
	seq := []Event{mk(0, 1, "a"), mk(0, 2, "b"), mk(0, 3, "c"), mk(0, 4, "d"), mk(0, 5, "e"), mk(0, 2, "b"), mk(0, 2, "z"), mk(0, -1, "n"), mk(0, 1, "a")}
	marks := "aaaaa=CNR"
	we := map[byte]error{'C': rec.ErrConflict, 'R': rec.ErrRewound, 'N': rec.ErrNegativeOffset}
	wk := map[byte]Kind{'a': Applied, '=': Idempotent}
	fail := errors.New("dedup: built-in self-check failed")
	var log []Event
	for i, e := range seq {
		r, err := d.Apply(e)
		if k, known := wk[marks[i]]; known {
			if err != nil || r.Kind != k {
				return fail
			}
			if marks[i] == 'a' {
				log = append(log, e)
			}
		} else if !errors.Is(err, we[marks[i]]) {
			return fail
		}
		if !reflect.DeepEqual(d.View(), naiveRebuild(4, log)) {
			return fail
		}
	}
	_, err := d.ApplyBatch([]Event{mk(0, 6, "g"), mk(0, -1, "x")})
	if err == nil || !reflect.DeepEqual(d.View(), naiveRebuild(4, log)) {
		return fail
	}
	return nil
}

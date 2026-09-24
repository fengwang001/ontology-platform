// Package api is the outward-facing sorted-snapshot differ. It keeps the
// current snapshot in process memory and emits a changelog per Advance.
// Dependencies point one way: api -> merge -> snap.
package api

import (
	"errors"
	"reflect"
	"sync"

	"ontology/merge"
	"ontology/snap"
)

// Change is one changelog entry; Kind is 'I' insert, 'D' delete, 'U' update.
type Change = merge.Change

// The four mutually distinct, decidable sentinel errors.
var (
	ErrDuplicateKey   = snap.ErrDuplicateKey // adjacent equal keys
	ErrNotSorted      = snap.ErrNotSorted    // key smaller than the previous row
	ErrMaxChanges     = errors.New("api: maxChanges must be positive")
	ErrTooManyChanges = errors.New("api: changelog exceeds maxChanges")
)

// Differ holds the current snapshot and cumulative stats behind a RWMutex.
type Differ struct {
	mu         sync.RWMutex
	current    []snap.Row
	maxChanges int
	advances   int // successful Advance calls
	total      int // cumulative emitted changes
}

// New validates config and the initial snapshot (duplicates included).
func New(initial []snap.Row, maxChanges int) (*Differ, error) {
	if maxChanges <= 0 {
		return nil, ErrMaxChanges
	}
	if err := snap.Validate(initial); err != nil {
		return nil, err
	}
	return &Differ{current: snap.Clone(initial), maxChanges: maxChanges}, nil
}

// Advance validates next, computes the whole changelog, and commits only
// after every check passes; a rejected call leaves state and stats intact.
func (d *Differ) Advance(next []snap.Row) ([]Change, error) {
	if err := snap.Validate(next); err != nil {
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	nc := snap.Clone(next)
	ch := merge.Diff(d.current, nc)
	if len(ch) > d.maxChanges {
		return nil, ErrTooManyChanges
	}
	d.current, d.advances, d.total = nc, d.advances+1, d.total+len(ch)
	return ch, nil
}

// Current returns a defensive copy of the current snapshot.
func (d *Differ) Current() []snap.Row {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return snap.Clone(d.current)
}

// Stats reports successful advances and cumulative change count.
func (d *Differ) Stats() (advances, totalChanges int) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.advances, d.total
}

// SelfCheck verifies the four NOTES.md invariants on a built-in sequence
// using a throwaway differ; it never mutates the receiver, so it is safe to
// call concurrently with Advance.
func (d *Differ) SelfCheck() error {
	if err := merge.SelfCheck(); err != nil {
		return err
	}
	r := func(k int64, v string) snap.Row { return snap.Row{Key: k, Val: v} }
	seq := [][]snap.Row{
		{r(1, "a"), r(3, "b")},
		{r(1, "a"), r(2, "i"), r(3, "B")}, // I2 + U3 (1 equal: no change)
		{r(2, "i"), r(3, "B")},            // D1
		nil,                               // D2 D3 tail
		{r(9, "x")},                       // I9 disjoint
	}
	l, err := New(seq[0], 1000)
	if err != nil {
		return err
	}
	for k := 0; k+1 < len(seq); k++ { // invariants 1,2,3 on every transition
		ch, err := l.Advance(seq[k+1])
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(merge.Replay(seq[k], ch), seq[k+1]) { // 1
			return errors.New("api: replay self-check failed")
		}
		if !reflect.DeepEqual(ch, merge.Naive(seq[k], seq[k+1])) { // 2
			return errors.New("api: naive-reference self-check failed")
		}
		for i, c := range ch { // 3: minimal and strictly key-sorted
			if (c.Kind == 'U' && c.Old == c.New) || (i > 0 && ch[i-1].Key >= c.Key) {
				return errors.New("api: minimality self-check failed")
			}
		}
	}
	before, bAdv, bTot := l.Current(), l.advances, l.total // 4: no trace on failure
	for _, c := range []struct {
		rows []snap.Row
		want error
	}{
		{[]snap.Row{r(1, "x"), r(1, "y")}, ErrDuplicateKey},
		{[]snap.Row{r(2, ""), r(1, "")}, ErrNotSorted}, // swapped pair
	} {
		if _, e := l.Advance(c.rows); !errors.Is(e, c.want) {
			return errors.New("api: expected a validation rejection")
		}
	}
	if _, e := New(seq[0], 0); !errors.Is(e, ErrMaxChanges) {
		return errors.New("api: expected config rejection")
	}
	lm, _ := New(nil, 2)
	if _, e := lm.Advance([]snap.Row{r(1, "v"), r(2, "v"), r(3, "v")}); !errors.Is(e, ErrTooManyChanges) {
		return errors.New("api: expected limit rejection")
	}
	a, t := l.Stats()
	if !reflect.DeepEqual(l.Current(), before) || a != bAdv || t != bTot || a != 4 || t != 6 {
		return errors.New("api: rejection left a trace")
	}
	if _, err := l.Advance(seq[0]); err != nil { // still usable afterwards
		return errors.New("api: unusable after rejection")
	}
	return nil
}

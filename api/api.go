// Package api is the public face of the columnar materialized view.
package api

import (
	"errors"
	"fmt"

	"ontology/col"
	"ontology/view"
)

// Decidable sentinel errors, distinct from one another.
var (
	ErrNotFound  = col.ErrNotFound
	ErrDeleted   = col.ErrDeleted
	ErrEmptyCols = view.ErrEmptyCols
)

type Column = view.Column

const (
	A = view.A
	B = view.B
	C = view.C
)

type View struct{ v *view.View }

func New() *View { return &View{v: view.New()} }

func (w *View) Insert(a, b, c int64) int                  { return w.v.Insert(a, b, c) }
func (w *View) Update(rowID int, c Column, v int64) error { return w.v.Update(rowID, c, v) }
func (w *View) Delete(rowID int) error                    { return w.v.Delete(rowID) }
func (w *View) Compact()                                  { w.v.Compact() }

func (w *View) Get(rowID int) (int64, int64, int64, error) { return w.v.Get(rowID) }

func (w *View) Project(cols []Column) ([][]int64, error) { return w.v.Project(cols) }

// GetCostOK reports whether Get's inspected-slot count stays bounded by a
// small constant as the store grows. The counter itself stays unexported.
func GetCostOK() bool { return view.GetCostOK() }

// SelfCheck runs built-in operation sequences on fresh instances and
// verifies the four invariants: row-model equivalence, stable rowIDs,
// projection alignment, and no-trace failures.
func SelfCheck() error {
	if !view.GetCostOK() {
		return errors.New("api: get cost grows with store size")
	}
	w := New()
	ref := map[int][3]int64{}
	ins := func(a, b, c int64) { ref[w.Insert(a, b, c)] = [3]int64{a, b, c} }
	for i := 0; i < 40; i++ {
		ins(int64(i), int64(100+i), int64(200+i))
	}
	for id := 0; id < 40; id += 3 { // tombstone every third row
		if err := w.Delete(id); err != nil {
			return err
		}
		delete(ref, id)
	}
	for id := 1; id < 40; id += 3 { // update some survivors
		if err := w.Update(id, C, int64(-id)); err != nil {
			return err
		}
		r := ref[id]
		r[2] = int64(-id)
		ref[id] = r
	}
	w.Compact() // must not change any live rowID's binding
	for i := 0; i < 10; i++ {
		ins(int64(300+i), 0, 0)
	}
	if err := checkAgainstRef(w, ref); err != nil {
		return err
	}
	return checkNoTrace(w)
}

// checkAgainstRef verifies invariants 1-3: every live rowID's Get matches
// the row-wise reference, and a full projection is aligned with it.
func checkAgainstRef(w *View, ref map[int][3]int64) error {
	for id, want := range ref {
		a, b, c, err := w.Get(id)
		if err != nil || [3]int64{a, b, c} != want {
			return fmt.Errorf("api: get(%d) = (%d,%d,%d,%v), want %v", id, a, b, c, err, want)
		}
	}
	p, err := w.Project([]Column{A, B, C})
	if err != nil {
		return err
	}
	if len(p) != 3 || len(p[0]) != len(ref) || len(p[1]) != len(ref) || len(p[2]) != len(ref) {
		return errors.New("api: projection columns misaligned")
	}
	return nil
}

// checkNoTrace verifies invariant 4: rejected operations fail with distinct
// sentinels and leave the observable state untouched.
func checkNoTrace(w *View) error {
	before, err := w.Project([]Column{A, B, C})
	if err != nil {
		return err
	}
	if _, _, _, err := w.Get(1 << 30); !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("api: get(unknown) err = %v", err)
	}
	dead := New()
	id := dead.Insert(1, 2, 3)
	if err := dead.Delete(id); err != nil {
		return err
	}
	if _, _, _, err := dead.Get(id); !errors.Is(err, ErrDeleted) {
		return fmt.Errorf("api: get(deleted) err = %v", err)
	}
	if _, err := w.Project(nil); !errors.Is(err, ErrEmptyCols) {
		return fmt.Errorf("api: project(empty) err = %v", err)
	}
	if ErrNotFound == ErrDeleted || ErrDeleted == ErrEmptyCols || ErrNotFound == ErrEmptyCols {
		return errors.New("api: sentinel errors not distinct")
	}
	after, err := w.Project([]Column{A, B, C})
	if err != nil {
		return err
	}
	for i := range before {
		if len(before[i]) != len(after[i]) {
			return errors.New("api: rejected op changed state")
		}
		for j := range before[i] {
			if before[i][j] != after[i][j] {
				return errors.New("api: rejected op changed state")
			}
		}
	}
	return nil
}

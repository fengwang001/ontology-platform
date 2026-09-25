// Package view adds rowID allocation, column-subset projection and error
// mapping on top of the columnar storage in package col.
package view

import (
	"errors"
	"sync"

	"ontology/col"
)

var ErrEmptyCols = errors.New("view: empty column set")

// Column names one of the three fixed columns.
type Column int

const (
	A Column = iota
	B
	C
)

// View allocates rowIDs and serves reads over a col.Store.
type View struct {
	mu     sync.Mutex // serializes rowID allocation with the store append
	nextID int
	store  *col.Store
}

func New() *View { return &View{store: &col.Store{}} }

// Insert appends a new row and returns its freshly allocated rowID.
func (v *View) Insert(a, b, c int64) int {
	v.mu.Lock()
	defer v.mu.Unlock()
	id := v.nextID
	v.nextID++
	v.store.Insert(id, a, b, c)
	return id
}

func colIndex(c Column) (int, error) {
	if c < A || c > C {
		return -1, col.ErrBadCol
	}
	return int(c), nil
}

func (v *View) Update(rowID int, c Column, val int64) error {
	idx, err := colIndex(c)
	if err != nil {
		return err
	}
	return v.store.Update(rowID, idx, val)
}

func (v *View) Delete(rowID int) error { return v.store.Delete(rowID) }

func (v *View) Get(rowID int) (int64, int64, int64, error) {
	return v.store.Get(rowID)
}

func (v *View) Compact() { v.store.Compact() }

// Project returns one slice per requested column (in request order), each
// holding the live rows' values in slot order. Every column is cut from the
// same snapshot, so element i of every column comes from the same row.
// The column set must be non-empty; rejection happens before any read.
func (v *View) Project(cols []Column) ([][]int64, error) {
	if len(cols) == 0 {
		return nil, ErrEmptyCols
	}
	idxs := make([]int, len(cols))
	for i, c := range cols {
		idx, err := colIndex(c)
		if err != nil {
			return nil, err
		}
		idxs[i] = idx
	}
	snap := v.store.Snapshot()
	out := make([][]int64, len(idxs))
	for i, idx := range idxs {
		out[i] = append([]int64(nil), snap[idx]...)
	}
	return out, nil
}

// GetCostOK exposes col's bounded-get-cost verdict (never the counter).
func GetCostOK() bool { return col.GetCostOK() }

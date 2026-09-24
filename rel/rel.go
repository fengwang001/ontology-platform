// Package rel maintains the two-sided row multiset of a single join key
// and produces the incremental FULL OUTER JOIN change log for that key.
package rel

import (
	"errors"
	"maps"
	"slices"
)

// Null marks the NULL-padded side of a padding row.
const Null = "-"

var (
	// ErrDuplicate: the row id already exists on that side.
	ErrDuplicate = errors.New("rel: duplicate row id")
	// ErrNotFound: the row id does not exist on that side.
	ErrNotFound = errors.New("rel: row id not found")
)

// Row is one materialized output row; Null denotes a NULL-padded side.
type Row struct {
	Key string
	Lid string
	Rid string
}

// Change is one change-log entry: Add=true inserts Row, Add=false retracts it.
type Change struct {
	Add bool
	Row
}

// Rel holds the left/right row-id sets of one key.
type Rel struct {
	key   string
	left  map[string]struct{}
	right map[string]struct{}
}

// New returns an empty Rel for key.
func New(key string) *Rel {
	return &Rel{key: key, left: map[string]struct{}{}, right: map[string]struct{}{}}
}

// Clone returns a deep copy.
func (r *Rel) Clone() *Rel {
	c := New(r.key)
	maps.Copy(c.left, r.left)
	maps.Copy(c.right, r.right)
	return c
}

// Empty reports whether both sides are empty.
func (r *Rel) Empty() bool { return len(r.left) == 0 && len(r.right) == 0 }

func sorted(m map[string]struct{}) []string { return slices.Sorted(maps.Keys(m)) }

// row builds an output row; leftSide tells which column m belongs to.
func (r *Rel) row(leftSide bool, m, t string) Row {
	if leftSide {
		return Row{r.key, m, t}
	}
	return Row{r.key, t, m}
}

// View recomputes the materialized rows of this key from scratch.
func (r *Rel) View() []Row {
	var out []Row
	ls, rs := sorted(r.left), sorted(r.right)
	switch {
	case len(ls) > 0 && len(rs) > 0:
		for _, l := range ls {
			for _, rr := range rs {
				out = append(out, Row{r.key, l, rr})
			}
		}
	case len(ls) > 0:
		for _, l := range ls {
			out = append(out, Row{r.key, l, Null})
		}
	default:
		for _, rr := range rs {
			out = append(out, Row{r.key, Null, rr})
		}
	}
	return out
}

// put inserts id into mine. leftSide tells which output column mine maps to.
func (r *Rel) put(mine, theirs map[string]struct{}, id string, leftSide bool) ([]Change, error) {
	if _, ok := mine[id]; ok {
		return nil, ErrDuplicate
	}
	m0, t0 := len(mine), len(theirs)
	mine[id] = struct{}{}
	row := func(m, t string) Row { return r.row(leftSide, m, t) }
	var out []Change
	switch {
	case t0 == 0: // other side empty: padding row
		out = append(out, Change{true, row(id, Null)})
	case m0 == 0: // my side crosses 0 -> non-zero: switch padding to pairs
		for _, t := range sorted(theirs) {
			out = append(out, Change{false, row(Null, t)})
		}
		for _, t := range sorted(theirs) {
			out = append(out, Change{true, row(id, t)})
		}
	default: // non-crossing: just add my pairs
		for _, t := range sorted(theirs) {
			out = append(out, Change{true, row(id, t)})
		}
	}
	return out, nil
}

// del removes id from mine. leftSide tells which output column mine maps to.
func (r *Rel) del(mine, theirs map[string]struct{}, id string, leftSide bool) ([]Change, error) {
	if _, ok := mine[id]; !ok {
		return nil, ErrNotFound
	}
	m0, t0 := len(mine), len(theirs)
	delete(mine, id)
	row := func(m, t string) Row { return r.row(leftSide, m, t) }
	if t0 == 0 { // other side empty: retract my padding row
		return []Change{{false, row(id, Null)}}, nil
	}
	var out []Change
	for _, t := range sorted(theirs) {
		out = append(out, Change{false, row(id, t)})
	}
	if m0 == 1 { // my side crosses to zero: switch pairs to padding
		for _, t := range sorted(theirs) {
			out = append(out, Change{true, row(Null, t)})
		}
	}
	return out, nil
}

// PutLeft inserts a left row id.
func (r *Rel) PutLeft(id string) ([]Change, error) { return r.put(r.left, r.right, id, true) }

// PutRight inserts a right row id.
func (r *Rel) PutRight(id string) ([]Change, error) { return r.put(r.right, r.left, id, false) }

// DelLeft deletes a left row id.
func (r *Rel) DelLeft(id string) ([]Change, error) { return r.del(r.left, r.right, id, true) }

// DelRight deletes a right row id.
func (r *Rel) DelRight(id string) ([]Change, error) { return r.del(r.right, r.left, id, false) }

// Package gagg maintains the row table and per-group aggregates, validates
// ops, and applies batches atomically. It depends only on gdelta.
package gagg

import (
	"errors"
	"maps"
	"sync"

	"ontology/gdelta"
)

// Sentinel errors: callers must distinguish all four rejections.
var (
	ErrRowExists   = errors.New("gagg: row already exists")
	ErrRowNotFound = errors.New("gagg: row not found")
	ErrEmptyGroup  = errors.New("gagg: group key must be non-empty")
	ErrTooMany     = errors.New("gagg: group count exceeds maxGroups")
)

// Table is the in-memory row table, aggregates and emitted changelog.
type Table struct {
	mu        sync.Mutex
	rows      map[int64]gdelta.Row
	agg       map[string]gdelta.Agg
	log       []gdelta.Change
	maxGroups int
	// reads counts rows read to update aggregates in the latest batch;
	// unexported, never reachable through the public API.
	reads int64
}

// New creates an empty table bounded by maxGroups groups.
func New(maxGroups int) *Table {
	return &Table{rows: map[int64]gdelta.Row{}, agg: map[string]gdelta.Agg{}, maxGroups: maxGroups}
}

// Apply applies one batch atomically and returns its changelog entries.
// Any rejected op rejects the whole batch: no state change, no log append.
func (t *Table) Apply(ops []gdelta.Op) ([]gdelta.Change, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	type im struct { // pre-mutation image: agg group (row=false) or row entry
		g       string
		a       gdelta.Agg
		id      int64
		r       gdelta.Row
		ok, row bool
	}
	var h []im
	rb := func() {
		for i := len(h) - 1; i >= 0; i-- { // reverse-restore images
			u := h[i]
			switch {
			case u.row && u.ok:
				t.rows[u.id] = u.r
			case u.row:
				delete(t.rows, u.id)
			case u.ok:
				t.agg[u.g] = u.a
			default:
				delete(t.agg, u.g)
			}
		}
	}
	out := []gdelta.Change{}
	t.reads = 0
	bad := func(e error) ([]gdelta.Change, error) { rb(); return nil, e }

	for _, op := range ops {
		t.reads++ // every op reads exactly its own id's row
		var old, nw *gdelta.Row
		if op.Kind != gdelta.DeleteOp && op.G == "" {
			return bad(ErrEmptyGroup)
		}
		r, ex := t.rows[op.ID]
		switch op.Kind {
		case gdelta.InsertOp:
			if ex {
				return bad(ErrRowExists)
			}
			nw = &gdelta.Row{G: op.G, V: op.V}
		case gdelta.UpdateOp:
			if !ex {
				return bad(ErrRowNotFound)
			}
			old, nw = &r, &gdelta.Row{G: op.G, V: op.V}
		case gdelta.DeleteOp:
			if !ex {
				return bad(ErrRowNotFound)
			}
			old = &r
		default:
			return bad(ErrRowNotFound)
		}
		for _, d := range gdelta.Deltas(old, nw) {
			bef, aft := t.agg[d.G], gdelta.Apply(t.agg[d.G], d)
			av, ex := t.agg[d.G]
			h = append(h, im{g: d.G, a: av, ok: ex})
			out = append(out, gdelta.Entries(d.G, bef, aft)...)
			if aft.Count > 0 {
				t.agg[d.G] = aft
			} else {
				delete(t.agg, d.G)
			}
		}
		if len(t.agg) > t.maxGroups {
			return bad(ErrTooMany)
		}
		h = append(h, im{id: op.ID, r: r, ok: ex, row: true})
		if nw != nil {
			t.rows[op.ID] = *nw
		} else {
			delete(t.rows, op.ID)
		}
	}
	t.log = append(t.log, out...)
	return out, nil
}

// View returns a copy of the materialized view (count > 0 groups only).
func (t *Table) View() map[string]gdelta.Agg {
	t.mu.Lock()
	defer t.mu.Unlock()
	return maps.Clone(t.agg)
}

// CheckReadsBounded grows one group to several scales m and fails unless a
// same-key Update, a key-change Update and a Delete each read O(1) rows.
// Only a verdict is exposed, never the counter value.
func CheckReadsBounded() error {
	for _, m := range []int64{100, 1000, 10000} {
		t := New(2)
		for i := int64(1); i <= m; i++ {
			if _, e := t.Apply([]gdelta.Op{gdelta.Insert(i, "g", 1)}); e != nil {
				return e
			}
		}
		if _, e := t.Apply([]gdelta.Op{gdelta.Insert(0, "h", 1)}); e != nil {
			return e
		}
		for _, op := range []gdelta.Op{gdelta.Update(1, "g", 2), gdelta.Update(2, "h", 2), gdelta.Delete(3)} {
			if _, e := t.Apply([]gdelta.Op{op}); e != nil || t.reads > 2 {
				return errors.New("gagg: aggregate update scanned more than O(1) rows")
			}
		}
	}
	return nil
}

// Package lagview maintains per-partition LAG values and emits a changelog.
package lagview

import "ontology/lagord"

// Change is one changelog entry: +(ID, Lag) or, when Del, -(ID, Lag).
// A nil Lag means NULL ("no predecessor"), strictly distinct from 0.
type Change struct {
	Del bool
	ID  int64
	Lag *int64
}

type ent struct {
	part string
	sort int64
	val  int64
}

// View maintains LAG(Val) OVER (PARTITION BY part ORDER BY sort, id).
type View struct {
	parts map[string]*lagord.Seq
	byID  map[int64]ent
}

// New returns an empty View.
func New() *View {
	return &View{parts: map[string]*lagord.Seq{}, byID: map[int64]ent{}}
}

// Has reports whether id currently exists.
func (v *View) Has(id int64) bool { _, ok := v.byID[id]; return ok }

// Len returns the total row count across partitions.
func (v *View) Len() int { return len(v.byID) }

// EqLag reports whether two LAG values are equal; nil means NULL.
func EqLag(a, b *int64) bool { return a == b || a != nil && b != nil && *a == *b }

// ApplyChange applies one changelog entry to a downstream model and reports
// whether it was consistent: - must retract the current value, + needs an absent id.
func ApplyChange(down map[int64]*int64, c Change) bool {
	cur, ok := down[c.ID]
	if c.Del && (!ok || !EqLag(cur, c.Lag)) || !c.Del && ok {
		return false
	}
	if c.Del {
		delete(down, c.ID)
	} else {
		down[c.ID] = c.Lag
	}
	return true
}

// lagOf returns the LAG of the row at index i: the previous row's Val,
// or nil (NULL) for the first row of the partition.
func lagOf(s *lagord.Seq, i int) *int64 {
	if i == 0 {
		return nil
	}
	val := s.At(i - 1).Val
	return &val
}

// Insert adds a row and returns this step's changelog: +(new row), then
// -(old) +(new) for the successor only if its LAG actually changed.
func (v *View) Insert(id int64, part string, sort, val int64) []Change {
	s := v.parts[part]
	if s == nil {
		s = &lagord.Seq{}
		v.parts[part] = s
	}
	i := s.Insert(lagord.Row{ID: id, Sort: sort, Val: val})
	v.byID[id] = ent{part, sort, val}
	out := []Change{{ID: id, Lag: lagOf(s, i)}}
	if i+1 < s.Len() { // the successor's old LAG was the new row's LAG
		if old, newLag := lagOf(s, i), &val; !EqLag(old, newLag) {
			out = append(out, Change{Del: true, ID: s.At(i + 1).ID, Lag: old},
				Change{ID: s.At(i + 1).ID, Lag: newLag})
		}
	}
	return out
}

// Delete removes id and returns this step's changelog: -(deleted row),
// then -(old) +(new) for the successor only if its LAG actually changed.
func (v *View) Delete(id int64) []Change {
	e := v.byID[id]
	s := v.parts[e.part]
	i, _ := s.Locate(e.sort, id)
	cur := lagOf(s, i)
	out := []Change{{Del: true, ID: id, Lag: cur}}
	if i+1 < s.Len() { // the successor's old LAG was the deleted row's Val
		if old, newLag := &e.val, cur; !EqLag(old, newLag) {
			out = append(out, Change{Del: true, ID: s.At(i + 1).ID, Lag: old},
				Change{ID: s.At(i + 1).ID, Lag: newLag})
		}
	}
	s.RemoveAt(i)
	delete(v.byID, id)
	if s.Len() == 0 {
		delete(v.parts, e.part)
	}
	return out
}

// Batch recomputes the full materialized view from scratch: every row's
// LAG is its predecessor's Val within its partition.
func (v *View) Batch() map[int64]*int64 {
	out := make(map[int64]*int64, len(v.byID))
	for _, s := range v.parts {
		for i := 0; i < s.Len(); i++ {
			out[s.At(i).ID] = lagOf(s, i)
		}
	}
	return out
}

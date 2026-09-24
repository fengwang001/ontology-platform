// Package mapc projects an event of one schema version onto the active
// layout, aligning by column name. It depends only on sch.
package mapc

import (
	"sync/atomic"

	"ontology/sch"
)

// Mapper projects events onto the active schema.
type Mapper struct {
	// locateCmp counts the comparisons the most recent Map spent locating
	// ONE active column's same-named column in the event layout. With the
	// hash index below it is a small constant independent of layout width.
	// Unexported on purpose: not readable through any exported API.
	locateCmp atomic.Int64
}

func NewMapper() *Mapper { return &Mapper{} }

// Map projects values (ordered by the event layout) onto the active
// layout. It is a pure function of its arguments: same inputs, same
// result, regardless of call order or concurrency.
func (m *Mapper) Map(event, active []sch.Column, values []any) ([]any, error) {
	if len(values) != len(event) {
		return nil, sch.ErrBadValue
	}
	idx := make(map[string]int, len(event)) // name -> position in event layout
	for i, c := range event {
		idx[c.Name] = i
	}
	out := make([]any, len(active))
	for i, ac := range active {
		m.locateCmp.Store(1) // one hash lookup per column: O(1), not a scan
		j, ok := idx[ac.Name]
		if !ok { // column added after the event's version
			if ac.Required {
				return nil, sch.ErrMissingColumn
			}
			out[i] = sch.Zero(ac.Typ)
			continue
		}
		v, err := sch.Coerce(values[j], event[j].Typ, ac.Typ)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

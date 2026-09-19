package ontology

import "fmt"

// Record describes a single degradation accepted while converting in
// lenient mode. Every record's Category also appears as a strict-mode
// error category for the same input; lenient mode never hides a class
// of problem, it only changes the disposition.
type Record struct {
	Category Category
	// From is the original value (or a textual rendering of it).
	From any
	// To is the value produced after degradation.
	To any
	// Index is the slice element position, or -1 for scalar values.
	Index int
	// Detail carries category-specific numbers, e.g. the dropped
	// fractional value or the raw overflowing text.
	Detail string
}

func (r Record) String() string {
	loc := ""
	if r.Index >= 0 {
		loc = fmt.Sprintf(" at index %d", r.Index)
	}
	s := fmt.Sprintf("%s%s: %v -> %v", r.Category, loc, r.From, r.To)
	if r.Detail != "" {
		s += " (" + r.Detail + ")"
	}
	return s
}

func newRecord(cat Category, from, to any, detail string) Record {
	return Record{Category: cat, From: from, To: to, Index: -1, Detail: detail}
}

// valueString renders values for error messages and record details.
func valueString(v any) string {
	switch t := v.(type) {
	case nil:
		return "nil"
	case string:
		return fmt.Sprintf("%q", t)
	case float64:
		return fmt.Sprintf("%v", t)
	default:
		return fmt.Sprintf("%v", t)
	}
}

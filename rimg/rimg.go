// Package rimg holds row-image validation, whole-row equality and event
// shape checks. It depends on no other package.
package rimg

// Row is a row image: column name -> string value. The PK lives outside the
// image on the Event.
type Row map[string]string

// Kind is the CDC event kind.
type Kind uint8

const (
	Insert Kind = iota + 1
	Update
	Delete
)

// Event is one upstream CDC record. Before/After presence depends on Kind.
type Event struct {
	Seq    int64
	Kind   Kind
	PK     int64
	Before Row
	After  Row
}

// ValidRow reports whether the image is usable: at least one column and no
// empty column name.
func ValidRow(r Row) bool {
	if len(r) == 0 {
		return false
	}
	for k := range r {
		if k == "" {
			return false
		}
	}
	return true
}

// Equal reports whole-row equality: the column-name sets must be identical and
// every value equal. A missing column is NOT equal to an empty-string value:
// with equal lengths, a key absent from b is caught by the comma-ok lookup.
func Equal(a, b Row) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

// ValidEvent checks the before/after shape required by each Kind:
// Insert needs After only, Update needs both, Delete needs Before only.
func ValidEvent(e Event) bool {
	switch e.Kind {
	case Insert:
		return e.Before == nil && ValidRow(e.After)
	case Update:
		return ValidRow(e.Before) && ValidRow(e.After)
	case Delete:
		return e.After == nil && ValidRow(e.Before)
	default:
		return false
	}
}

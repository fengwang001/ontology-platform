// Package rimg holds CDC row images, events and their pure shape logic:
// image legality, full-row equality and event-shape validation.
// It depends on no other package.
package rimg

import "errors"

// Row is a full row image: column name -> value. The PK lives outside it.
type Row map[string]string

// Kind is the CDC event kind.
type Kind int

const (
	Insert Kind = iota + 1
	Update
	Delete
)

func (k Kind) String() string {
	switch k {
	case Insert:
		return "Insert"
	case Update:
		return "Update"
	case Delete:
		return "Delete"
	default:
		return "Unknown"
	}
}

// ConflictType classifies a skipped event.
type ConflictType int

const (
	RowExists      ConflictType = iota + 1 // Insert hits an existing PK
	RowMissing                             // Update/Delete hits an absent PK
	BeforeMismatch                         // current row is not fully equal to Before
)

func (c ConflictType) String() string {
	switch c {
	case RowExists:
		return "行已存在"
	case RowMissing:
		return "行不存在"
	case BeforeMismatch:
		return "前像不符"
	default:
		return "未知冲突"
	}
}

// Event is one CDC record. Before/After are full row images.
type Event struct {
	Seq    int64
	Kind   Kind
	PK     int64
	Before Row
	After  Row
}

// Conflict is one conflict-log entry.
type Conflict struct {
	Seq  int64
	PK   int64
	Type ConflictType
}

// Result is the per-event outcome of an Apply.
type Result struct {
	Seq      int64
	Applied  bool
	Conflict ConflictType // meaningful only when Applied == false
}

// ErrInvalidEvent is the sentinel for any illegal event shape.
var ErrInvalidEvent = errors.New("rimg: invalid event shape")

// ValidRow reports whether m is a legal image: at least one column and no
// empty column name. Values are always strings, including "".
func ValidRow(m Row) bool {
	if len(m) == 0 {
		return false
	}
	for col := range m {
		if col == "" {
			return false
		}
	}
	return true
}

// RowEqual reports full-row equality: identical column sets AND identical
// values. A missing column is not equal to an empty-string value.
func RowEqual(a, b Row) bool {
	if len(a) != len(b) {
		return false
	}
	for col, va := range a {
		vb, ok := b[col]
		if !ok || vb != va {
			return false
		}
	}
	return true
}

// ValidEvent enforces per-kind image presence and image legality:
// Insert needs After only; Update needs both; Delete needs Before only.
func ValidEvent(e Event) error {
	hasB, hasA := e.Before != nil, e.After != nil
	switch e.Kind {
	case Insert:
		if hasB || !hasA || !ValidRow(e.After) {
			return ErrInvalidEvent
		}
	case Update:
		if !hasB || !hasA || !ValidRow(e.Before) || !ValidRow(e.After) {
			return ErrInvalidEvent
		}
	case Delete:
		if !hasB || hasA || !ValidRow(e.Before) {
			return ErrInvalidEvent
		}
	default:
		return ErrInvalidEvent
	}
	return nil
}

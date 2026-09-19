package ontology

import (
	"fmt"
	"strconv"
	"strings"
)

// PartKind classifies one column of a group key so that the three
// "missing" situations stay distinguishable and never merge.
type PartKind uint8

const (
	// PartAbsent: the attribute does not exist in the row.
	PartAbsent PartKind = iota
	// PartNil: the attribute exists but its value is nil.
	PartNil
	// PartEmpty: the attribute exists and is the empty string.
	PartEmpty
	// PartValue: the attribute exists with a usable value.
	PartValue
)

// KeyPart is one column of a group key.
type KeyPart struct {
	Kind  PartKind
	Value any // meaningful only when Kind == PartValue
}

// String renders the part unambiguously, e.g. for error messages.
func (p KeyPart) String() string {
	switch p.Kind {
	case PartAbsent:
		return "<absent>"
	case PartNil:
		return "<nil>"
	case PartEmpty:
		return "<empty>"
	default:
		return fmt.Sprintf("%v", p.Value)
	}
}

// keyPartsFromRow extracts the ordered group key for one row.
// Absent / nil / empty-string each map to their own PartKind and are
// never merged; a row is never dropped because of a missing key column.
func keyPartsFromRow(row map[string]any, attrs []string) []KeyPart {
	parts := make([]KeyPart, len(attrs))
	for i, attr := range attrs {
		v, ok := row[attr]
		switch {
		case !ok:
			parts[i] = KeyPart{Kind: PartAbsent}
		case v == nil:
			parts[i] = KeyPart{Kind: PartNil}
		default:
			if s, isStr := v.(string); isStr && s == "" {
				parts[i] = KeyPart{Kind: PartEmpty}
			} else {
				parts[i] = KeyPart{Kind: PartValue, Value: v}
			}
		}
	}
	return parts
}

// encodeParts encodes a key into a collision-free string usable as a
// map key. Each segment is length-prefixed, so no separator ambiguity
// can arise between columns or between kinds and values.
func encodeParts(parts []KeyPart) string {
	var b strings.Builder
	for _, p := range parts {
		s := p.sortToken()
		fmt.Fprintf(&b, "%d:%s;", len(s), s)
	}
	return b.String()
}

// sortToken returns a canonical string for one part. The leading digit
// is the PartKind, which fixes the sort position of the missing kinds:
// absent < nil < empty < any present value.
func (p KeyPart) sortToken() string {
	if p.Kind != PartValue {
		return strconv.Itoa(int(p.Kind))
	}
	return strconv.Itoa(int(PartValue)) + "|" + valueToken(p.Value)
}

// valueToken renders a present value with a type tag so that, e.g.,
// the string "1" and the int64 1 never collide. fmt prints maps with
// sorted keys, so the rendering is deterministic for any value.
func valueToken(v any) string {
	return fmt.Sprintf("%T|%v", v, v)
}

// compareParts orders two keys column by column. Missing kinds sort by
// their PartKind (absent, then nil, then empty) before all present
// values; present values sort by their canonical token. The result is
// total and independent of map iteration order.
func compareParts(a, b []KeyPart) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i].Kind != b[i].Kind {
			return int(a[i].Kind) - int(b[i].Kind)
		}
		if a[i].Kind == PartValue {
			ta, tb := valueToken(a[i].Value), valueToken(b[i].Value)
			if ta != tb {
				if ta < tb {
					return -1
				}
				return 1
			}
		}
	}
	return len(a) - len(b)
}

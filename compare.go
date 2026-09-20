package ontology

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

// cell is the precomputed sortable value of one row for one key.
type cell struct {
	val  any
	null bool
}

// extractCell reads field from row and classifies it. Missing keys,
// nil values, and NaN are all null for sorting purposes. The second
// return value reports whether the value was a NaN (counted by Sort).
func extractCell(row map[string]any, field string) (cell, bool) {
	v, ok := row[field]
	if !ok || v == nil {
		return cell{null: true}, false
	}
	if f, isFloat := v.(float64); isFloat && math.IsNaN(f) {
		return cell{null: true}, true
	}
	return cell{val: v}, false
}

// IncomparableError is returned when two rows hold values of
// incompatible types for the same key. It identifies the key and both
// concrete types, so callers can decide how to react.
type IncomparableError struct {
	Key      string
	KeyIndex int
	TypeA    string
	TypeB    string
}

// Error implements error.
func (e *IncomparableError) Error() string {
	return fmt.Sprintf("ontology: sort key %q (#%d): incomparable types %s and %s",
		e.Key, e.KeyIndex, e.TypeA, e.TypeB)
}

// errIncomparable is an internal sentinel wrapped into IncomparableError.
var errIncomparable = errors.New("ontology: incomparable values")

// compareCells orders two cells for one key. Null placement depends
// only on NullsFirst; Desc negates value comparison only.
func compareCells(a, b cell, key SortKey, keyIndex int) (int, error) {
	if a.null || b.null {
		switch {
		case a.null && b.null:
			return 0, nil
		case a.null:
			if key.NullsFirst {
				return -1, nil
			}
			return 1, nil
		default:
			if key.NullsFirst {
				return 1, nil
			}
			return -1, nil
		}
	}
	cmp, err := compareValues(a.val, b.val)
	if err != nil {
		return 0, &IncomparableError{
			Key:      key.Field,
			KeyIndex: keyIndex,
			TypeA:    fmt.Sprintf("%T", a.val),
			TypeB:    fmt.Sprintf("%T", b.val),
		}
	}
	if key.Desc {
		cmp = -cmp
	}
	return cmp, nil
}

// compareValues totally orders supported non-null values: strings
// lexicographically, bools false<true, and int64/float64 numerically
// across the two types. Anything else is an error, never a fallback
// to type-name ordering.
func compareValues(a, b any) (int, error) {
	if an, ok := asNumber(a); ok {
		bn, ok := asNumber(b)
		if !ok {
			return 0, errIncomparable
		}
		switch {
		case an < bn:
			return -1, nil
		case an > bn:
			return 1, nil
		default:
			return 0, nil
		}
	}
	switch av := a.(type) {
	case string:
		bv, ok := b.(string)
		if !ok {
			return 0, errIncomparable
		}
		return strings.Compare(av, bv), nil
	case bool:
		bv, ok := b.(bool)
		if !ok {
			return 0, errIncomparable
		}
		switch {
		case av == bv:
			return 0, nil
		case !av:
			return -1, nil
		default:
			return 1, nil
		}
	}
	return 0, errIncomparable
}

// asNumber converts int64 and float64 to a common numeric domain.
// +0.0 and -0.0 compare equal here, so stability decides their order.
func asNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case int64:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

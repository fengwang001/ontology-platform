// Package row defines a single ordered row and its composite sort key.
package row

import "math"

// Row is one record: a float64 sort value plus a globally unique string ID.
type Row struct {
	Value float64
	ID    string
}

// Compare is the total order on composite keys (Value, ID):
// -1 when a < b, 0 when a == b (same Value and same ID), +1 when a > b.
// Values are ordered by their numeric float64 value; ties break by ID bytes.
func Compare(a, b Row) int {
	switch {
	case a.Value < b.Value || (a.Value == b.Value && a.ID < b.ID):
		return -1
	case a.Value == b.Value && a.ID == b.ID:
		return 0
	default:
		return 1
	}
}

// Key is the cursor-relevant part of a row: the composite sort key.
type Key struct {
	Value float64
	ID    string
}

// Key returns the composite key carried by a row.
func (r Row) Key() Key { return Key{Value: r.Value, ID: r.ID} }

// CompareKeys compares two keys using the same total order as Compare.
func CompareKeys(a, b Key) int {
	return Compare(Row{Value: a.Value, ID: a.ID}, Row{Value: b.Value, ID: b.ID})
}

// Less reports whether a sorts strictly before b.
func Less(a, b Key) bool { return CompareKeys(a, b) < 0 }

// Equal reports whether two keys are identical.
func Equal(a, b Key) bool { return CompareKeys(a, b) == 0 }

// Valid reports whether a value is usable as a sort key. NaN is rejected:
// NaN participates in no total order, so it must never enter the dataset.
func (k Key) Valid() bool { return !math.IsNaN(k.Value) && k.ID != "" }

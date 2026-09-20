package ontology

import "strconv"

// Value is a property value: either NULL or a string.
// NULL and the empty string are distinct values.
type Value struct {
	Null bool
	Str  string
}

// Null returns the NULL value.
func Null() Value { return Value{Null: true} }

// Str returns a non-NULL string value.
func Str(s string) Value { return Value{Str: s} }

// String renders the value for diagnostics; NULL prints as "NULL".
func (v Value) String() string {
	if v.Null {
		return "NULL"
	}
	return strconv.Quote(v.Str)
}

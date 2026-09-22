// Package zone maintains per-row-group statistics (min, max, null count) and
// decides whether a predicate can possibly match a row group. It depends on
// no other package in this module.
package zone

// Kind identifies which of the three mutually distinguishable states a Value
// is in: NULL, an integer, or a string. NULL is a state of its own, never a
// representation of 0 or "".
type Kind uint8

const (
	Null Kind = iota
	Int
	Str
)

// Value is the three-state reading result: null, int64, or string.
// The zero Value is NULL; 0 and "" are constructed explicitly and remain
// distinguishable from NULL by Kind.
type Value struct {
	Kind Kind
	I    int64
	S    string
}

// NullValue returns the NULL value.
func NullValue() Value { return Value{Kind: Null} }

// IntValue returns an integer value (0 included).
func IntValue(i int64) Value { return Value{Kind: Int, I: i} }

// StringValue returns a string value ("" included).
func StringValue(s string) Value { return Value{Kind: Str, S: s} }

// IsNull reports whether v is NULL.
func (v Value) IsNull() bool { return v.Kind == Null }

// Equal is a strict equality across the three states: NULL equals only NULL,
// integers and strings of different Kinds never compare equal.
func (v Value) Equal(o Value) bool {
	if v.Kind != o.Kind {
		return false
	}
	switch v.Kind {
	case Int:
		return v.I == o.I
	case Str:
		return v.S == o.S
	default:
		return true
	}
}

// Compare returns -1/0/1 for two values of the same Kind. It panics on NULL
// or mixed Kinds, which must not be compared by callers.
func Compare(a, b Value) int {
	if a.Kind != b.Kind {
		panic("zone: compare across kinds")
	}
	switch a.Kind {
	case Int:
		switch {
		case a.I < b.I:
			return -1
		case a.I > b.I:
			return 1
		}
		return 0
	case Str:
		switch {
		case a.S < b.S:
			return -1
		case a.S > b.S:
			return 1
		}
		return 0
	default:
		panic("zone: compare of null")
	}
}

package coercion

import "fmt"

// Kind identifies a supported property target type.
type Kind int

const (
	// String targets a Go string.
	String Kind = iota + 1
	// Int64 targets a Go int64.
	Int64Kind
	// Float64Kind targets a Go float64.
	Float64Kind
	// BoolKind targets a Go bool.
	BoolKind
	// StringSlice targets []string.
	StringSlice
	// Int64Slice targets []int64.
	Int64Slice
	// Float64Slice targets []float64.
	Float64Slice
	// BoolSlice targets []bool.
	BoolSlice
)

func (k Kind) String() string {
	switch k {
	case String:
		return "String"
	case Int64Kind:
		return "Int64"
	case Float64Kind:
		return "Float64"
	case BoolKind:
		return "Bool"
	case StringSlice:
		return "[]String"
	case Int64Slice:
		return "[]Int64"
	case Float64Slice:
		return "[]Float64"
	case BoolSlice:
		return "[]Bool"
	default:
		return fmt.Sprintf("Kind(%d)", int(k))
	}
}

func (k Kind) element() Kind {
	switch k {
	case StringSlice:
		return String
	case Int64Slice:
		return Int64Kind
	case Float64Slice:
		return Float64Kind
	case BoolSlice:
		return BoolKind
	default:
		return 0
	}
}

func (k Kind) isSlice() bool {
	return k.element() != 0
}

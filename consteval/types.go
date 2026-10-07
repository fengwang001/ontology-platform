// Package consteval implements a constant-expression evaluator for a
// statically typed language: callers build structured constant expression
// trees, and the evaluator computes value, kind and type under untyped
// arbitrary-precision semantics with per-step representability checks for
// typed constants.
package consteval

import "fmt"

// Kind is the value category of a constant.
type Kind int

const (
	IntKind Kind = iota
	RatKind
	BoolKind
	StringKind
)

func (k Kind) String() string {
	switch k {
	case IntKind:
		return "int"
	case RatKind:
		return "rat"
	case BoolKind:
		return "bool"
	case StringKind:
		return "string"
	}
	return "unknown-kind"
}

// Type is a concrete type of a typed constant. NoType marks untyped values.
type Type int

const (
	NoType Type = iota
	Int8
	Int16
	Int32
	Int64
	Uint8
	Uint16
	Uint32
	Uint64
	Float64
	Bool
	String
)

var typeNames = map[string]Type{
	"int8": Int8, "int16": Int16, "int32": Int32, "int64": Int64,
	"uint8": Uint8, "uint16": Uint16, "uint32": Uint32, "uint64": Uint64,
	"float64": Float64, "bool": Bool, "string": String,
}

// ParseType resolves a type name. An empty name yields NoType (untyped).
// Unknown names are a structural error reported as ErrInvalidArgument.
func ParseType(name string) (Type, error) {
	if name == "" {
		return NoType, nil
	}
	if t, ok := typeNames[name]; ok {
		return t, nil
	}
	return NoType, &Error{Kind: ErrInvalidArgument, Msg: fmt.Sprintf("unknown type name %q", name)}
}

func (t Type) String() string {
	for name, tt := range typeNames {
		if tt == t {
			return name
		}
	}
	return "untyped"
}

// IsInt reports whether t is a sized integer type.
func (t Type) IsInt() bool {
	return t >= Int8 && t <= Uint64
}

// IsSigned reports whether t is a signed integer type.
func (t Type) IsSigned() bool {
	return t >= Int8 && t <= Int64
}

// IsUnsigned reports whether t is an unsigned integer type.
func (t Type) IsUnsigned() bool {
	return t >= Uint8 && t <= Uint64
}

// Width returns the bit width of a sized integer type (0 otherwise).
func (t Type) Width() uint {
	switch t {
	case Int8, Uint8:
		return 8
	case Int16, Uint16:
		return 16
	case Int32, Uint32:
		return 32
	case Int64, Uint64:
		return 64
	}
	return 0
}

// KindOf maps a concrete type to its value kind.
func (t Type) KindOf() Kind {
	switch {
	case t.IsInt():
		return IntKind
	case t == Float64:
		return RatKind
	case t == Bool:
		return BoolKind
	case t == String:
		return StringKind
	}
	return IntKind
}

// DefaultType is the concrete type assumed for an untyped constant when a
// context requires one: int64 for integers, float64 for rationals, bool and
// string for themselves.
func DefaultType(k Kind) Type {
	switch k {
	case IntKind:
		return Int64
	case RatKind:
		return Float64
	case BoolKind:
		return Bool
	case StringKind:
		return String
	}
	return NoType
}

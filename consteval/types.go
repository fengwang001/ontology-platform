// Package consteval implements a constant-expression evaluator for a
// static language. Callers build structured constant-expression trees and
// evaluate them under untyped arbitrary-precision semantics with
// per-step representability checks for typed constants. A registry stores
// named constants that later expressions may reference.
package consteval

import "math/big"

// Kind is the kind of an (untyped) constant value.
type Kind int

const (
	KindInvalid Kind = iota
	KindInt
	KindRational
	KindBool
	KindString
)

func (k Kind) String() string {
	switch k {
	case KindInt:
		return "int"
	case KindRational:
		return "rational"
	case KindBool:
		return "bool"
	case KindString:
		return "string"
	}
	return "invalid"
}

// Type is a concrete type of a typed constant. TypeNone means untyped.
type Type int

const (
	TypeNone Type = iota
	TypeInt8
	TypeInt16
	TypeInt32
	TypeInt64
	TypeUint8
	TypeUint16
	TypeUint32
	TypeUint64
	TypeFloat64
	TypeBool
	TypeString
)

var typeNames = map[string]Type{
	"int8":    TypeInt8,
	"int16":   TypeInt16,
	"int32":   TypeInt32,
	"int64":   TypeInt64,
	"uint8":   TypeUint8,
	"uint16":  TypeUint16,
	"uint32":  TypeUint32,
	"uint64":  TypeUint64,
	"float64": TypeFloat64,
	"bool":    TypeBool,
	"string":  TypeString,
}

// ParseType resolves a type name; ok is false for unknown names.
func ParseType(name string) (t Type, ok bool) {
	t, ok = typeNames[name]
	return t, ok
}

func (t Type) String() string {
	switch t {
	case TypeNone:
		return "untyped"
	case TypeInt8:
		return "int8"
	case TypeInt16:
		return "int16"
	case TypeInt32:
		return "int32"
	case TypeInt64:
		return "int64"
	case TypeUint8:
		return "uint8"
	case TypeUint16:
		return "uint16"
	case TypeUint32:
		return "uint32"
	case TypeUint64:
		return "uint64"
	case TypeFloat64:
		return "float64"
	case TypeBool:
		return "bool"
	case TypeString:
		return "string"
	}
	return "unknown"
}

// IsInt reports whether t is a signed or unsigned integer type.
func (t Type) IsInt() bool {
	return t >= TypeInt8 && t <= TypeUint64
}

// Signed reports whether t is a signed integer type.
func (t Type) Signed() bool {
	return t >= TypeInt8 && t <= TypeInt64
}

// Bits returns the bit width of an integer type.
func (t Type) Bits() int {
	switch t {
	case TypeInt8, TypeUint8:
		return 8
	case TypeInt16, TypeUint16:
		return 16
	case TypeInt32, TypeUint32:
		return 32
	case TypeInt64, TypeUint64:
		return 64
	}
	return 0
}

// intRange returns the inclusive [min, max] bounds of an integer type.
func intRange(t Type) (min, max *big.Int) {
	w := uint(t.Bits())
	max = new(big.Int).Lsh(big.NewInt(1), w)
	if t.Signed() {
		// [-2^(w-1), 2^(w-1)-1]
		half := new(big.Int).Rsh(max, 1)
		return new(big.Int).Neg(half), new(big.Int).Sub(half, big.NewInt(1))
	}
	// [0, 2^w - 1]
	return new(big.Int), new(big.Int).Sub(max, big.NewInt(1))
}

// DefaultType maps a kind to its default concrete type, used in contexts
// that require a concrete type (such as declaring a named constant
// without an explicit type).
func DefaultType(k Kind) Type {
	switch k {
	case KindInt:
		return TypeInt64
	case KindRational:
		return TypeFloat64
	case KindBool:
		return TypeBool
	case KindString:
		return TypeString
	}
	return TypeNone
}

// category is the internal operational category of a constant, unifying
// untyped kinds and typed values for operator-legality checks.
type category int

const (
	catInvalid category = iota
	catInt
	catRat
	catFloat
	catBool
	catString
)

func numericCat(c category) bool {
	return c == catInt || c == catRat || c == catFloat
}

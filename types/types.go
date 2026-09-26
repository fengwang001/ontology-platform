// Package types defines the two types of the simple type system: int and bool.
// It depends on no other package in this module.
package types

// Kind is the unexported discriminator of a type.
type Kind uint8

const (
	kInt Kind = iota + 1
	kBool
)

// T is a type. The zero value is not a valid type; use Int or Bool.
type T struct {
	k Kind
}

// Int and Bool are the only two types in the system.
var (
	Int  = T{kInt}
	Bool = T{kBool}
)

// Equal reports whether two types are the same. There is no subtyping.
func (t T) Equal(o T) bool { return t.k == o.k }

// String returns the human-readable name of the type.
func (t T) String() string {
	switch t.k {
	case kInt:
		return "int"
	case kBool:
		return "bool"
	default:
		return "<invalid type>"
	}
}

// Package types defines the two types of the expression language.
package types

// T is a type: either Int or Bool.
type T int

const (
	Int T = iota
	Bool
)

// String returns a human-readable name: "int" or "bool".
func (t T) String() string {
	if t == Int {
		return "int"
	}
	return "bool"
}

// Equal reports whether t and u are the same type.
func (t T) Equal(u T) bool { return t == u }

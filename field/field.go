// Package field defines the tagged value union and per-field rules:
// required/type/default legality. It depends on nothing else.
package field

import "errors"

// Kind is the tag of the value union.
type Kind int

const (
	Int Kind = iota
	Str
	Bool
)

// Value is a tagged union: Kind selects exactly one of I, S, B.
type Value struct {
	Kind Kind
	I    int64
	S    string
	B    bool
}

func IntVal(i int64) Value  { return Value{Kind: Int, I: i} }
func StrVal(s string) Value { return Value{Kind: Str, S: s} }
func BoolVal(b bool) Value  { return Value{Kind: Bool, B: b} }

// Sentinel errors. The three rejection categories — illegal schema,
// missing required field, type mismatch — are mutually distinct.
var (
	ErrEmptyFieldName     = errors.New("field: empty field name")
	ErrDuplicateField     = errors.New("field: duplicate field name")
	ErrRequiredHasDefault = errors.New("field: required field must not have a default")
	ErrOptionalNoDefault  = errors.New("field: optional field must have a default")
	ErrDefaultType        = errors.New("field: default type differs from declared type")
	ErrMissingRequired    = errors.New("field: missing required field")
	ErrTypeMismatch       = errors.New("field: value type mismatch")
)

// Field describes one schema field. Default is nil iff Required is true.
type Field struct {
	Name     string
	Type     Kind
	Required bool
	Default  *Value
}

// CheckDef validates a single field definition.
func CheckDef(f Field) error {
	if f.Name == "" {
		return ErrEmptyFieldName
	}
	if f.Required && f.Default != nil {
		return ErrRequiredHasDefault
	}
	if !f.Required && f.Default == nil {
		return ErrOptionalNoDefault
	}
	if f.Default != nil && f.Default.Kind != f.Type {
		return ErrDefaultType
	}
	return nil
}

// CheckValue reports ErrTypeMismatch when v's tag differs from f.Type.
// No type conversion is ever performed.
func CheckValue(f Field, v Value) error {
	if v.Kind != f.Type {
		return ErrTypeMismatch
	}
	return nil
}

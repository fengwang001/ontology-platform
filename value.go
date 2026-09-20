// Package ontology implements a unique-constraint checker with
// configurable key normalization. Normalization is used only for
// comparison; stored and returned values are always the originals.
package ontology

// Value is a property value. It distinguishes NULL (database-style
// absent value) from the empty string: the two are never equal.
type Value struct {
	s   string
	set bool
}

// Null returns the NULL value.
func Null() Value { return Value{} }

// Str returns a non-NULL string value.
func Str(s string) Value { return Value{s: s, set: true} }

// IsNull reports whether the value is NULL.
func (v Value) IsNull() bool { return !v.set }

// String returns the underlying string. It is "" for NULL; use
// IsNull to tell NULL and "" apart.
func (v Value) String() string { return v.s }

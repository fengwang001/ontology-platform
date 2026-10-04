package schema

import "errors"

// ErrInvalidFields is returned by NewVersion and Project when the field list
// or the projection name list violates the structural constraints.
var ErrInvalidFields = errors.New("invalid fields")

type Mode int

const (
	Backward Mode = iota + 1
	Forward
	Full
)

// ValidMode reports whether m is one of the declared compatibility modes.
func ValidMode(m Mode) bool { return m >= Backward && m <= Full }

type Type int

const (
	Int32 Type = iota + 1
	Int64
	Float64
	String
	Bytes
)

// ValidType reports whether t is a supported field type.
func ValidType(t Type) bool { return t >= Int32 && t <= Bytes }

type Field struct {
	Name       string
	Type       Type
	Required   bool
	HasDefault bool
}

// View is any ordered field set readable by compat.CanRead.
type View interface {
	Fields() []Field
	FieldByName(name string) (Field, bool)
}

// Version is an immutable ordered list of 1..64 uniquely named fields.
type Version struct {
	fields []Field
	byName map[string]int
}

// NewVersion validates fields and returns an immutable version.
func NewVersion(fields []Field) (*Version, error) {
	if len(fields) < 1 || len(fields) > 64 {
		return nil, ErrInvalidFields
	}
	clone := make([]Field, len(fields))
	byName := make(map[string]int, len(fields))
	for i, f := range fields {
		if f.Name == "" || !ValidType(f.Type) {
			return nil, ErrInvalidFields
		}
		if _, dup := byName[f.Name]; dup {
			return nil, ErrInvalidFields
		}
		byName[f.Name] = i
		clone[i] = f
	}
	return &Version{fields: clone, byName: byName}, nil
}

// Fields returns a defensive copy of the ordered field list.
func (v *Version) Fields() []Field {
	out := make([]Field, len(v.fields))
	copy(out, v.fields)
	return out
}

// FieldByName looks a field up by name in O(1).
func (v *Version) FieldByName(name string) (Field, bool) {
	i, ok := v.byName[name]
	if !ok {
		return Field{}, false
	}
	return v.fields[i], true
}

// Equal reports exact ordered field-by-field equality.
func (v *Version) Equal(other *Version) bool {
	if len(v.fields) != len(other.fields) {
		return false
	}
	for i := range v.fields {
		if v.fields[i] != other.fields[i] {
			return false
		}
	}
	return true
}

// Projection is a version restricted to a subset of its fields,
// keeping the original version's field order.
type Projection struct {
	fields []Field
	byName map[string]int
}

// Project returns v projected onto names: names must be non-empty, contain
// 1..64 unique entries that all exist in v. Output order follows v.
func Project(v *Version, names []string) (*Projection, error) {
	if len(names) < 1 || len(names) > 64 {
		return nil, ErrInvalidFields
	}
	seen := make(map[string]struct{}, len(names))
	for _, n := range names {
		if n == "" {
			return nil, ErrInvalidFields
		}
		if _, dup := seen[n]; dup {
			return nil, ErrInvalidFields
		}
		seen[n] = struct{}{}
	}
	picked := make(map[string]Field, len(names))
	for _, n := range names {
		f, ok := v.FieldByName(n)
		if !ok {
			return nil, ErrInvalidFields
		}
		picked[n] = f
	}
	fields := make([]Field, 0, len(names))
	byName := make(map[string]int, len(names))
	for _, f := range v.fields {
		if _, ok := picked[f.Name]; ok {
			byName[f.Name] = len(fields)
			fields = append(fields, picked[f.Name])
		}
	}
	return &Projection{fields: fields, byName: byName}, nil
}

// Fields returns the projected ordered fields.
func (p *Projection) Fields() []Field {
	out := make([]Field, len(p.fields))
	copy(out, p.fields)
	return out
}

// FieldByName looks a projected field up by name.
func (p *Projection) FieldByName(name string) (Field, bool) {
	i, ok := p.byName[name]
	if !ok {
		return Field{}, false
	}
	return p.fields[i], true
}

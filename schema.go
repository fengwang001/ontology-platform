// Package ontology implements schema registration and versioned evolution
// for an ontology platform. All state is kept in process memory.
package ontology

import "fmt"

// DataType is the type of a property value.
type DataType string

const (
	TypeString  DataType = "string"
	TypeInteger DataType = "integer"
	TypeFloat   DataType = "float"
	TypeBoolean DataType = "boolean"
)

// PropertyType describes a single property of an ObjectType.
type PropertyType struct {
	Name       string
	Type       DataType
	Required   bool
	PrimaryKey bool
}

// ObjectType is a named collection of properties. Property names must be
// unique within an ObjectType and exactly one property must be the primary
// key.
type ObjectType struct {
	Name       string
	Properties []PropertyType
}

// InvalidObjectTypeError reports a structurally invalid ObjectType
// definition (empty name, duplicate property names, wrong primary key
// count, or an empty property name).
type InvalidObjectTypeError struct {
	ObjectType string
	Property   string
	Reason     string
}

func (e *InvalidObjectTypeError) Error() string {
	if e.Property != "" {
		return fmt.Sprintf("ontology: invalid object type %q: property %q: %s", e.ObjectType, e.Property, e.Reason)
	}
	return fmt.Sprintf("ontology: invalid object type %q: %s", e.ObjectType, e.Reason)
}

// validate checks the structural invariants of the ObjectType.
func (ot ObjectType) validate() error {
	if ot.Name == "" {
		return &InvalidObjectTypeError{ObjectType: ot.Name, Reason: "name must not be empty"}
	}
	seen := make(map[string]bool, len(ot.Properties))
	primaryKeys := 0
	for _, p := range ot.Properties {
		if p.Name == "" {
			return &InvalidObjectTypeError{ObjectType: ot.Name, Reason: "property name must not be empty"}
		}
		if seen[p.Name] {
			return &InvalidObjectTypeError{ObjectType: ot.Name, Property: p.Name, Reason: "duplicate property name"}
		}
		seen[p.Name] = true
		if p.PrimaryKey {
			primaryKeys++
		}
	}
	if primaryKeys != 1 {
		return &InvalidObjectTypeError{
			ObjectType: ot.Name,
			Reason:     fmt.Sprintf("exactly one primary key property is required, got %d", primaryKeys),
		}
	}
	return nil
}

// property returns the property with the given name.
func (ot ObjectType) property(name string) (PropertyType, bool) {
	for _, p := range ot.Properties {
		if p.Name == name {
			return p, true
		}
	}
	return PropertyType{}, false
}

// primaryKey returns the name of the primary key property, or "" if none
// exists (which validate rules out).
func (ot ObjectType) primaryKey() string {
	for _, p := range ot.Properties {
		if p.PrimaryKey {
			return p.Name
		}
	}
	return ""
}

// clone returns a deep copy of the ObjectType.
func (ot ObjectType) clone() ObjectType {
	out := ObjectType{Name: ot.Name, Properties: make([]PropertyType, len(ot.Properties))}
	copy(out.Properties, ot.Properties)
	return out
}

// Schema is an immutable snapshot of all ObjectTypes at a given version.
type Schema struct {
	Version     int
	ObjectTypes map[string]ObjectType
}

// clone returns a deep copy of the Schema.
func (s *Schema) clone() *Schema {
	out := &Schema{
		Version:     s.Version,
		ObjectTypes: make(map[string]ObjectType, len(s.ObjectTypes)),
	}
	for name, ot := range s.ObjectTypes {
		out.ObjectTypes[name] = ot.clone()
	}
	return out
}

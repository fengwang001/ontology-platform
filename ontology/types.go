// Package ontology implements read-time adjudication for the ontology
// platform: row-level visibility predicates, property-level read/write and
// masking rules, and the symmetric write-path enforcement.
package ontology

// TypeKind enumerates the declared property types supported by the platform.
type TypeKind string

const (
	KindString  TypeKind = "string"
	KindInt     TypeKind = "int"
	KindFloat   TypeKind = "float"
	KindBoolean TypeKind = "boolean"
)

// DeclaredType is the type contract attached to an object property.
// For KindString, EnumValues, when non-empty, restricts the allowed values.
// For KindInt/KindFloat, Min/Max bound the value; nil means unbounded on that
// side. Min/Max are ignored for strings and booleans.
type DeclaredType struct {
	Kind       TypeKind
	EnumValues []string
	Min        *float64
	Max        *float64
}

// PropertyDef declares one property of an ObjectType.
type PropertyDef struct {
	Name string
	Type DeclaredType
}

// ObjectType is a named collection of typed properties.
type ObjectType struct {
	Name       string
	Properties []PropertyDef

	propertyByName map[string]PropertyDef
}

// LookupProperty returns the definition of name and whether the property is
// declared on this type.
func (t *ObjectType) LookupProperty(name string) (PropertyDef, bool) {
	if t.propertyByName == nil {
		t.propertyByName = map[string]PropertyDef{}
		for _, p := range t.Properties {
			t.propertyByName[p.Name] = p
		}
	}
	p, ok := t.propertyByName[name]
	return p, ok
}

// PropertyNames returns the declared property names in declaration order.
func (t *ObjectType) PropertyNames() []string {
	names := make([]string, len(t.Properties))
	for i, p := range t.Properties {
		names[i] = p.Name
	}
	return names
}

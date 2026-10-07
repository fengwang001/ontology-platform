// Package ontology defines the common ontology model: object types,
// link types (directed / bidirectional), instances and identifiers.
package ontology

import (
	"errors"
	"regexp"
)

// Direction of a link type.
type Direction int

const (
	// Directed links may only be traversed from Tail to Head.
	Directed Direction = iota
	// Bidirectional links may be traversed in both directions.
	Bidirectional
)

// ID is the identifier of an object instance, object type or link type.
type ID = string

var (
	// ErrInvalidID is returned when an identifier does not match idPattern.
	ErrInvalidID = errors.New("ontology: invalid identifier")

	idPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)
)

// ValidateID reports whether id is a legal identifier: 1..128 characters
// drawn from [A-Za-z0-9_.:-]. Illegal identifiers are rejected before any
// graph lookup, so callers can never distinguish "missing" from "hidden".
func ValidateID(id ID) error {
	if !idPattern.MatchString(id) {
		return ErrInvalidID
	}
	return nil
}

// ObjectType defines a category of object instances.
type ObjectType struct {
	ID   ID
	Name string
}

// LinkType defines a category of links. A link connects an instance of
// FromType to an instance of ToType and has a Direction.
type LinkType struct {
	ID        ID
	FromType  ID
	ToType    ID
	Direction Direction
}

// ObjectInstance belongs to exactly one ObjectType.
type ObjectInstance struct {
	ID         ID
	ObjectType ID
}

// LinkInstance connects Tail to Head and belongs to one LinkType.
type LinkInstance struct {
	ID       ID
	LinkType ID
	Tail     ID
	Head     ID
}

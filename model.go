package ontology

import "regexp"

// ID is a well-formed object identifier.
type ID = string

// MaxIDLength is the inclusive upper bound of an identifier length.
const MaxIDLength = 128

var idPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.:-]{0,127}$`)

// ValidID reports whether id is a well-formed object identifier:
// it must start with an ASCII letter, contain only letters, digits and
// '_', '.', ':', '-', and be at most MaxIDLength characters long.
func ValidID(id string) bool {
	return idPattern != nil && idPattern.MatchString(id)
}

// ObjectType is a named type of an ontology object.
type ObjectType struct {
	Name string
}

// LinkType is a named type of an ontology link.
type LinkType struct {
	Name string
	// Directed distinguishes directed link types (orientation From->To
	// is meaningful) from undirected ones (adjacency is symmetric).
	Directed bool
}

// Direction is the orientation of a link relative to an endpoint.
type Direction int

const (
	// DirEither denotes an undirected link.
	DirEither Direction = iota
	// DirOut denotes a link leaving the reference endpoint (From=ref, To=other).
	DirOut
	// DirIn denotes a link entering the reference endpoint (From=other, To=ref).
	DirIn
)

// Link is a link instance between two objects. Self links (From == To)
// are legal and are never boundary dangling links.
type Link struct {
	ID   ID
	Type LinkType
	From ID
	To   ID
}

// Valid reports whether the link instance itself is well-formed.
func (l Link) Valid() bool {
	if l.Type.Name == "" || !ValidID(l.ID) || !ValidID(l.From) || !ValidID(l.To) {
		return false
	}
	return true
}

// DirectionFrom returns the direction of the link as seen from endpoint ref.
// For undirected link types it always returns DirEither.
func (l Link) DirectionFrom(ref ID) Direction {
	if !l.Type.Directed {
		return DirEither
	}
	if l.From == ref {
		return DirOut
	}
	return DirIn
}

// Object is an ontology object instance.
type Object struct {
	ID   ID
	Type ObjectType
}

// Valid reports whether the object instance itself is well-formed.
func (o Object) Valid() bool {
	return o.Type.Name != "" && ValidID(o.ID)
}

// Permission models the existence permission: a principal holding it may
// learn about the existence of an object. It is the only permission kind
// consulted by snapshot extraction.
type Permission struct {
	Principal string
	Object    ID
}

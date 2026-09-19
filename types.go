package ontology

// Cardinality describes how many links may touch each endpoint of a LinkType.
type Cardinality int

const (
	// OneToOne: a source has at most one link and a target is taken by at most one source.
	OneToOne Cardinality = iota
	// OneToMany: a source may have many links, a target belongs to at most one source.
	OneToMany
	// ManyToMany: no cardinality restriction.
	ManyToMany
)

func (c Cardinality) String() string {
	switch c {
	case OneToOne:
		return "ONE_TO_ONE"
	case OneToMany:
		return "ONE_TO_MANY"
	case ManyToMany:
		return "MANY_TO_MANY"
	}
	return "UNKNOWN"
}

// CascadeMode decides what happens to a link (and its far endpoint) when an
// endpoint object is deleted.
type CascadeMode int

const (
	// Cascade deletes the object on the other end of the link, recursively.
	Cascade CascadeMode = iota
	// SetNull removes the link but keeps the object on the other end.
	SetNull
	// Restrict rejects the deletion while any such link exists.
	Restrict
)

func (m CascadeMode) String() string {
	switch m {
	case Cascade:
		return "CASCADE"
	case SetNull:
		return "SET_NULL"
	case Restrict:
		return "RESTRICT"
	}
	return "UNKNOWN"
}

// LinkType declares a named, directed relationship between two object types.
// Source and Target may be the same object type (self-referencing links).
type LinkType struct {
	Name        string
	Source      string // source ObjectType name
	Target      string // target ObjectType name
	Cardinality Cardinality
	// SourceRequired: every object of type Source must have >=1 outgoing link.
	SourceRequired bool
	// TargetRequired: every object of type Target must have >=1 incoming link.
	TargetRequired bool
	// OnDelete applies when either endpoint object is deleted.
	OnDelete CascadeMode
}

// linkRef identifies one concrete link instance.
type linkRef struct {
	linkType string
	source   string
	target   string
}

// PathStep is one hop in a cascade path, from object From across LinkType to To.
type PathStep struct {
	From     string
	LinkType string
	To       string
}

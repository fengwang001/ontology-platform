package ontology

// ObjectTypeID identifies an object type in the ontology.
type ObjectTypeID string

// ObjectID identifies an object instance.
type ObjectID string

// LinkTypeID identifies a link type.
type LinkTypeID string

// PropertyID identifies a property of an object type. Each link end
// references the property that represents "participating in this link".
type PropertyID string

// SubjectID identifies a principal performing an operation.
type SubjectID string

// Cardinality declares the allowed interval [Min, Max] for the number of
// links of one link type held by one instance at one end.
// A negative Max means unbounded.
type Cardinality struct {
	Min int
	Max int
}

// allowsAfterCreate reports whether holding n links after a creation is
// permitted. Only the upper bound is checked on creation: lower bounds
// cannot be satisfied incrementally (see DESIGN.md).
func (c Cardinality) allowsAfterCreate(n int) bool { return c.Max < 0 || n <= c.Max }

// allowsAfterDelete reports whether holding n links after a deletion is
// permitted. Only the lower bound is checked on deletion.
func (c Cardinality) allowsAfterDelete(n int) bool { return n >= c.Min }

// LinkEnd describes one end of a link type.
type LinkEnd struct {
	// ObjectType is the object type allowed at this end.
	ObjectType ObjectTypeID
	// Property is the participation property of the object type for this
	// link type. Its per-instance write permission decides whether a
	// subject may add or remove a link at this end; its read permission
	// decides visibility during cascade cleanup.
	Property PropertyID
	// Card is the cardinality constraint for instances at this end.
	Card Cardinality
	// CascadeDelete, when true, propagates a cascade deletion arriving
	// from the opposite end: once the connecting link is cleaned, the
	// instance at this end is deleted as well.
	CascadeDelete bool
}

// LinkType declares a relationship between two object types.
type LinkType struct {
	ID LinkTypeID
	// Directed declares directionality. For undirected link types the
	// endpoint pair is normalized so (a,b) and (b,a) denote the same link.
	Directed       bool
	Source, Target LinkEnd
}

// Permission is the independently declared read/write grant on one
// property of one object instance for one subject.
type Permission struct {
	Read  bool
	Write bool
}

// CascadeMode selects how cascade cleanup treats participation-property
// write permissions.
type CascadeMode int

const (
	// CascadeBypass executes cascade cleanup with the system's own
	// identity: no permission or visibility checks are performed.
	CascadeBypass CascadeMode = iota
	// CascadeCheck checks the initiating subject's permissions for every
	// link cleaned during a cascade. Links that fail a check are skipped
	// and reported distinctly.
	CascadeCheck
)

func (m CascadeMode) String() string {
	if m == CascadeCheck {
		return "check"
	}
	return "bypass"
}

// InvisiblePolicy selects how cascade cleanup reacts when a participation
// property of an instance being cleaned is not readable by the initiating
// subject. The policy is engine-wide and applies uniformly to every link
// type; it cannot be overridden per link type.
type InvisiblePolicy int

const (
	// InvisiblePartial skips only the cleanup related to the invisible
	// property; the rest of the deletion proceeds.
	InvisiblePartial InvisiblePolicy = iota
	// InvisibleAbort aborts the whole cascade deletion with
	// ClassCascadeAborted and applies no changes at all.
	InvisibleAbort
)

func (p InvisiblePolicy) String() string {
	if p == InvisibleAbort {
		return "abort"
	}
	return "partial"
}

// Config holds the engine-wide cascade behavior settings.
type Config struct {
	Cascade   CascadeMode
	Invisible InvisiblePolicy
}

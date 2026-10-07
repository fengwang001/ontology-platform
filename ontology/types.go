package ontology

// Value is an attribute value. The batch engine treats values opaquely;
// validation hooks decide whether a value is acceptable.
type Value = any

// Properties maps property name to value.
type Properties map[string]Value

// InstanceID uniquely identifies an object instance within the platform.
type InstanceID string

// TypeName identifies an object type or link type.
type TypeName string

// ValidationHook inspects a proposed property set for an instance of its
// registered object type. It returns a non-nil error to reject the change.
// current is nil when the operation creates a new instance; proposed is nil
// when the operation deletes an instance.
type ValidationHook func(current Properties, proposed Properties) error

// ObjectType defines a registered object type.
type ObjectType struct {
	Name TypeName
	// Hook, if set, runs for every proposed change to instances of this type.
	Hook ValidationHook
}

// Cardinality bounds one end of a link type.
type Cardinality struct {
	// Min and Max are inclusive bounds on the number of links incident to a
	// single instance at this end. Max < 0 means unbounded.
	Min int
	Max int
}

// LinkType defines an undirected, typed link between object instances.
// CardinalityLeft applies to each Left instance; CardinalityRight to each
// Right instance.
type LinkType struct {
	Name      TypeName
	LeftType  TypeName // object type of the A end
	RightType TypeName // object type of the B end
	CardA     Cardinality
	CardB     Cardinality
}

// Operation is one item of a batch: replace the full property set of one
// instance. Props nil means delete; an absent instance plus non-nil props
// means create.
type Operation struct {
	Instance    InstanceID
	Type        TypeName
	BaseVersion int64 // version the caller believes the instance currently has
	Props       Properties
}

// LinkOp mutates the link set. Props nil means delete; an absent instance plus non-nil props
// means create.
type LinkOp struct {
	Link TypeName
	A, B InstanceID
	// Add true: ensure link exists; false: ensure it is absent.
	Add bool
}

// Batch is one atomic unit of work.
type Batch struct {
	ID    string
	Ops   []Operation
	Links []LinkOp
}

// FailureClass enumerates the mutually exclusive batch failure reasons.
type FailureClass int

const (
	FailureNone FailureClass = iota
	FailureDuplicateWrite
	FailureVersionConflict
	FailureHookRejected
	FailureCardinality
)

// instance is the stored record for one object instance.
type instance struct {
	id      InstanceID
	typ     TypeName
	version int64
	props   Properties
}

// linkKey identifies a single undirected link instance.
type linkKey struct {
	link TypeName
	a, b InstanceID // normalized: a <= b
}

// BatchResult reports the outcome of a commit attempt.
type BatchResult struct {
	BatchID string
	OK      bool
	Failure FailureClass
	// Detail carries the first offending item (duplicate op / instance / etc).
	Detail string
	// NewVersions records post-commit versions per instance on success.
	NewVersions map[InstanceID]int64
	// CommitOrder is the global logical tick assigned on success.
	CommitTick int64
}

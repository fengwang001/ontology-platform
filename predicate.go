package ontology

// Predicate is a node of a predicate tree. Concrete implementations
// are Comparison, AndPred, OrPred, NotPred and IsNullPred.
type Predicate interface {
	isPredicate()
}

// Op is a comparison operator for leaf predicates.
type Op int

const (
	Eq Op = iota
	Lt
	Gt
)

func (o Op) String() string {
	switch o {
	case Eq:
		return "Eq"
	case Lt:
		return "Lt"
	default:
		return "Gt"
	}
}

// Comparison is a leaf: Attr <op> Value, where Value is a literal.
type Comparison struct {
	Op    Op
	Attr  string
	Value any
}

// AndPred is a conjunction evaluated left to right.
type AndPred struct {
	Left  Predicate
	Right Predicate
}

// OrPred is a disjunction evaluated left to right.
type OrPred struct {
	Left  Predicate
	Right Predicate
}

// NotPred negates its child.
type NotPred struct {
	Child Predicate
}

// IsNullPred is True when Attr is absent from the attribute set,
// False when present. It never yields Unknown.
type IsNullPred struct {
	Attr string
}

func (Comparison) isPredicate() {}
func (AndPred) isPredicate()    {}
func (OrPred) isPredicate()     {}
func (NotPred) isPredicate()    {}
func (IsNullPred) isPredicate() {}

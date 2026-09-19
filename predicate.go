package ontology

// Predicate is a node in an already-constructed predicate tree.
// The evaluator never mutates a Predicate.
type Predicate interface {
	predicateNode()
}

// Op identifies the kind of a comparison leaf.
type Op uint8

const (
	// Eq compares for equality.
	Eq Op = iota
	// Lt compares strictly less-than.
	Lt
	// Gt compares strictly greater-than.
	Gt
)

func (o Op) String() string {
	switch o {
	case Eq:
		return "eq"
	case Lt:
		return "lt"
	case Gt:
		return "gt"
	default:
		return "invalid-op"
	}
}

// Compare is a leaf predicate: Property <op> Literal.
// The left-hand side names an entity property; the right-hand side is
// a literal supplied at construction time.
type Compare struct {
	Property string
	Op       Op
	Literal  any
}

// And evaluates its children left to right with Kleene semantics.
type And struct {
	Children []Predicate
}

// Or evaluates its children left to right with Kleene semantics.
type Or struct {
	Children []Predicate
}

// Not negates its child; Not(Unknown) is Unknown.
type Not struct {
	Child Predicate
}

// IsNull is the only predicate that turns a missing property into a
// definite value: True when the property is absent, False otherwise.
// It never evaluates to Unknown.
type IsNull struct {
	Property string
}

func (*Compare) predicateNode() {}
func (*And) predicateNode()     {}
func (*Or) predicateNode()      {}
func (*Not) predicateNode()     {}
func (*IsNull) predicateNode()  {}

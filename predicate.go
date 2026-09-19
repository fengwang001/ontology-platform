package predicate

// Op identifies a leaf comparison operator.
type Op uint8

const (
	// Eq compares two values for equality.
	Eq Op = iota + 1
	// Lt is strict less-than.
	Lt
	// Gt is strict greater-than.
	Gt
)

func (op Op) String() string {
	switch op {
	case Eq:
		return "Eq"
	case Lt:
		return "Lt"
	case Gt:
		return "Gt"
	default:
		return "UnknownOp"
	}
}

// Node is a node in the predicate tree. Concrete types are *Compare,
// *IsNull, *Not and *AndOr.
//
// Left sides always name an entity attribute; right sides of comparisons are
// literal values. The evaluator never mutates a Node.
type Node interface{ predicateNode() }

// Compare is a leaf: attribute Op literal, e.g. age Lt int64(42).
type Compare struct {
	Attr string
	Op   Op
	RHS  any
}

// IsNull tests whether the named attribute is absent from the entity. It never
// evaluates to Unknown: missing => True, present => False.
type IsNull struct {
	Attr string
}

// Not holds a single child.
type Not struct {
	Child Node
}

// Kind selects conjunction or disjunction for an AndOr node.
type Kind uint8

const (
	// And combines children with three-valued logical AND.
	And Kind = iota + 1
	// Or combines children with three-valued logical OR.
	Or
)

// AndOr is an n-ary conjunction or disjunction. Children are evaluated
// strictly left to right.
type AndOr struct {
	Kind     Kind
	Children []Node
}

func (*Compare) predicateNode() {}
func (*IsNull) predicateNode()  {}
func (*Not) predicateNode()     {}
func (*AndOr) predicateNode()   {}

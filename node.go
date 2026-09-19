package predicate

// Op identifies a comparison operator on a leaf.
type Op int

const (
	Eq Op = iota
	Lt
	Gt
)

// String renders the operator.
func (o Op) String() string {
	switch o {
	case Lt:
		return "<"
	case Gt:
		return ">"
	default:
		return "=="
	}
}

// Node is a node in a predicate tree.
// Trees are constructed by the caller and never modified by the evaluator.
type Node interface {
	nodeMarker()
}

// Cmp is a leaf comparing the named attribute with a literal.
type Cmp struct {
	Attr string
	Op   Op
	Lit  any
}

// And evaluates children left to right with Kleene semantics.
type And struct{ Children []Node }

// Or evaluates children left to right with Kleene semantics.
type Or struct{ Children []Node }

// Not negates a single child.
type Not struct{ Child Node }

// IsNull is true exactly when the attribute is absent.
type IsNull struct{ Attr string }

func (*Cmp) nodeMarker()    {}
func (*And) nodeMarker()    {}
func (*Or) nodeMarker()     {}
func (*Not) nodeMarker()    {}
func (*IsNull) nodeMarker() {}

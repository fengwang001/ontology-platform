package ontology

// Pred is a node in a filter predicate tree. Concrete types: *Cmp, *AndNode,
// *OrNode, *NotNode and *IsNullNode. Trees are immutable during evaluation
// and safe to share across goroutines as long as callers do not mutate them.
type Pred interface {
	isPred()
}

// Op is a leaf comparison operator.
type Op uint8

const (
	// Eq compares attribute and literal for equality.
	Eq Op = iota
	// Lt compares attribute < literal.
	Lt
	// Gt compares attribute > literal.
	Gt
)

func (o Op) String() string {
	switch o {
	case Eq:
		return "Eq"
	case Lt:
		return "Lt"
	case Gt:
		return "Gt"
	default:
		return "Op(?)"
	}
}

// Cmp is a leaf comparison: attribute Attr compared by Op against the
// literal Value.
type Cmp struct {
	Op    Op
	Attr  string
	Value any
}

// AndNode is the conjunction of L and R, evaluated left to right.
type AndNode struct {
	L Pred
	R Pred
}

// OrNode is the disjunction of L and R, evaluated left to right.
type OrNode struct {
	L Pred
	R Pred
}

// NotNode negates P.
type NotNode struct {
	P Pred
}

// IsNullNode is True when Attr is absent from the attribute map and False
// when it is present. It never yields Unknown.
type IsNullNode struct {
	Attr string
}

func (*Cmp) isPred()        {}
func (*AndNode) isPred()    {}
func (*OrNode) isPred()     {}
func (*NotNode) isPred()    {}
func (*IsNullNode) isPred() {}

// Convenience constructors so callers can build trees without taking
// addresses of composite literals everywhere.

// EqTo builds an Eq leaf.
func EqTo(attr string, value any) *Cmp { return &Cmp{Op: Eq, Attr: attr, Value: value} }

// LtTo builds an Lt leaf.
func LtTo(attr string, value any) *Cmp { return &Cmp{Op: Lt, Attr: attr, Value: value} }

// GtTo builds a Gt leaf.
func GtTo(attr string, value any) *Cmp { return &Cmp{Op: Gt, Attr: attr, Value: value} }

// AndP builds an And node.
func AndP(l, r Pred) *AndNode { return &AndNode{L: l, R: r} }

// OrP builds an Or node.
func OrP(l, r Pred) *OrNode { return &OrNode{L: l, R: r} }

// NotP builds a Not node.
func NotP(p Pred) *NotNode { return &NotNode{P: p} }

// IsNull builds an IsNull node.
func IsNull(attr string) *IsNullNode { return &IsNullNode{Attr: attr} }

package ontology

// Op identifies the comparison operator of a leaf predicate.
type Op int

const (
	// OpEq tests equality.
	OpEq Op = iota
	// OpLt tests "less than".
	OpLt
	// OpGt tests "greater than".
	OpGt
)

// String returns a human-readable name of the operator.
func (o Op) String() string {
	switch o {
	case OpEq:
		return "Eq"
	case OpLt:
		return "Lt"
	case OpGt:
		return "Gt"
	default:
		return "Invalid"
	}
}

// NodeKind identifies the kind of a predicate tree node.
type NodeKind int

const (
	// KindCompare is a leaf comparison: attribute Op literal.
	KindCompare NodeKind = iota
	// KindAnd is a conjunction of children.
	KindAnd
	// KindOr is a disjunction of children.
	KindOr
	// KindNot negates its single child.
	KindNot
	// KindIsNull tests whether an attribute is absent.
	KindIsNull
)

// Predicate is a node of a predicate tree. Depending on Kind only a
// subset of the fields is meaningful:
//
//   - KindCompare: Attr, Op, Value
//   - KindAnd, KindOr: Children (at least one)
//   - KindNot: Children (exactly one)
//   - KindIsNull: Attr
type Predicate struct {
	Kind     NodeKind
	Attr     string
	Op       Op
	Value    any
	Children []*Predicate
}

// Eq builds a leaf predicate: attr == value.
func Eq(attr string, value any) *Predicate {
	return &Predicate{Kind: KindCompare, Attr: attr, Op: OpEq, Value: value}
}

// Lt builds a leaf predicate: attr < value.
func Lt(attr string, value any) *Predicate {
	return &Predicate{Kind: KindCompare, Attr: attr, Op: OpLt, Value: value}
}

// Gt builds a leaf predicate: attr > value.
func Gt(attr string, value any) *Predicate {
	return &Predicate{Kind: KindCompare, Attr: attr, Op: OpGt, Value: value}
}

// AndP builds a conjunction node.
func AndP(children ...*Predicate) *Predicate {
	return &Predicate{Kind: KindAnd, Children: children}
}

// OrP builds a disjunction node.
func OrP(children ...*Predicate) *Predicate {
	return &Predicate{Kind: KindOr, Children: children}
}

// NotP builds a negation node.
func NotP(child *Predicate) *Predicate {
	return &Predicate{Kind: KindNot, Children: []*Predicate{child}}
}

// IsNull builds a node testing whether attr is absent.
func IsNull(attr string) *Predicate {
	return &Predicate{Kind: KindIsNull, Attr: attr}
}

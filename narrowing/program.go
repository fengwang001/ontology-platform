package narrowing

// StatementID is a caller-supplied unique identifier for a statement;
// it doubles as a query point in the analysis result.
type StatementID string

// Stmt is one statement of a structured program.
type Stmt interface {
	StmtID() StatementID
	stmtNode()
}

// Assign assigns a type to a variable. Every normalized member of Type
// must belong to the variable's declared type; on success the
// variable's narrowed type becomes exactly Type.
type Assign struct {
	ID   StatementID
	Var  string
	Type TypeExpr
}

// StmtID implements Stmt.
func (s *Assign) StmtID() StatementID { return s.ID }
func (s *Assign) stmtNode()           {}

// If is a conditional branch. Either branch may be empty (nil).
type If struct {
	ID   StatementID
	Cond Cond
	Then []Stmt
	Else []Stmt
}

// StmtID implements Stmt.
func (s *If) StmtID() StatementID { return s.ID }
func (s *If) stmtNode()           {}

// Return terminates the current path.
type Return struct {
	ID StatementID
}

// StmtID implements Stmt.
func (s *Return) StmtID() StatementID { return s.ID }
func (s *Return) stmtNode()           {}

// TypeOfKind enumerates the type-test kinds. Note that the type test of
// null yields "object".
type TypeOfKind int

const (
	TypeOfNumber TypeOfKind = iota
	TypeOfString
	TypeOfBoolean
	TypeOfObject
	TypeOfUndefined
)

// LitKind enumerates literal kinds usable in strict equality tests.
type LitKind int

const (
	LitNumber LitKind = iota
	LitString
	LitBoolean
)

// Literal is a number, string or boolean literal.
type Literal struct {
	Kind LitKind
	Num  float64
	Str  string
	Bool bool
}

// NumLit builds a number literal.
func NumLit(v float64) Literal { return Literal{Kind: LitNumber, Num: v} }

// StrLit builds a string literal.
func StrLit(s string) Literal { return Literal{Kind: LitString, Str: s} }

// BoolLit builds a boolean literal.
func BoolLit(b bool) Literal { return Literal{Kind: LitBoolean, Bool: b} }

// Cond is a condition used by If statements.
type Cond interface{ condNode() }

// TypeOf tests the dynamic type of a variable.
type TypeOf struct {
	Var  string
	Kind TypeOfKind
}

// EqNull is the strict equality test against null.
type EqNull struct{ Var string }

// EqUndefined is the strict equality test against undefined.
type EqUndefined struct{ Var string }

// EqLiteral is the strict equality test against a literal.
type EqLiteral struct {
	Var string
	Lit Literal
}

// LooseEqNull is the loose equality test against null; it matches both
// null and undefined.
type LooseEqNull struct{ Var string }

// Truthy is the truthiness test of a variable itself.
type Truthy struct{ Var string }

// PropEq is the discriminant-property test: a strict equality of one
// property of an object variable against a literal.
type PropEq struct {
	Var  string
	Prop string
	Lit  Literal
}

// Not negates a condition.
type Not struct{ C Cond }

// And is the short-circuiting logical conjunction, evaluated left to
// right.
type And struct {
	L Cond
	R Cond
}

// Or is the short-circuiting logical disjunction, evaluated left to
// right.
type Or struct {
	L Cond
	R Cond
}

func (TypeOf) condNode()      {}
func (EqNull) condNode()      {}
func (EqUndefined) condNode() {}
func (EqLiteral) condNode()   {}
func (LooseEqNull) condNode() {}
func (Truthy) condNode()      {}
func (PropEq) condNode()      {}
func (Not) condNode()         {}
func (And) condNode()         {}
func (Or) condNode()          {}

// Decl declares one variable with its declared type.
type Decl struct {
	Name string
	Type TypeExpr
}

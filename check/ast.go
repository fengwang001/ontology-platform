package check

import (
	"sync/atomic"

	"ontology/types"
)

// Kind enumerates AST node kinds.
type Kind int

const (
	IntLit  Kind = iota // integer literal
	BoolLit             // true / false
	Var                 // identifier
	Neg                 // unary -
	Not                 // unary !
	Bin                 // binary Op
	If                  // A=cond B=then C=else
	Let                 // Name bound to A inside B
)

// Expr is a parsed expression AST node.
type Expr struct {
	Kind    Kind
	Op      string // Bin: + - * / < > <= >= == != && ||
	Name    string // Var: identifier; Let: binder
	Int     int64  // IntLit value
	Bool    bool   // BoolLit value
	A, B, C *Expr  // unary: A; Bin: A,B; If: A,B,C; Let: A=value, B=body
}

func IntL(v int64) *Expr               { return &Expr{Kind: IntLit, Int: v} }
func BoolL(v bool) *Expr               { return &Expr{Kind: BoolLit, Bool: v} }
func VarE(n string) *Expr              { return &Expr{Kind: Var, Name: n} }
func NegE(a *Expr) *Expr               { return &Expr{Kind: Neg, A: a} }
func NotE(a *Expr) *Expr               { return &Expr{Kind: Not, A: a} }
func BinE(op string, a, b *Expr) *Expr { return &Expr{Kind: Bin, Op: op, A: a, B: b} }
func IfE(c, t, f *Expr) *Expr          { return &Expr{Kind: If, A: c, B: t, C: f} }
func LetE(n string, v, b *Expr) *Expr  { return &Expr{Kind: Let, Name: n, A: v, B: b} }

// Env maps names to types. Extend returns a new Env and never mutates the
// receiver, so a failed check leaves no trace and shared envs stay read-only.
type Env struct {
	parent *Env
	m      map[string]types.T
	probes atomic.Int64 // bindings inspected by lookups; unexported by design
}

// NewEnv returns an empty environment.
func NewEnv() *Env { return &Env{m: map[string]types.T{}} }

// NewEnvFrom returns an environment holding all given bindings in one scope.
func NewEnvFrom(m map[string]types.T) *Env { return &Env{m: m} }

// Extend returns a child environment with name bound to t.
func (e *Env) Extend(name string, t types.T) *Env {
	return &Env{parent: e, m: map[string]types.T{name: t}}
}

// Lookup returns the type bound to name, or ok=false if undeclared.
func (e *Env) Lookup(name string) (types.T, bool) {
	t, ok, _ := e.probe(name)
	return t, ok
}

// probe walks the scope chain; each map access counts as one inspected
// binding (hash-map probe, independent of how many names the scope holds).
func (e *Env) probe(name string) (types.T, bool, int) {
	n := 0
	for x := e; x != nil; x = x.parent {
		n++
		if t, ok := x.m[name]; ok {
			e.probes.Add(int64(n))
			return t, true, n
		}
	}
	e.probes.Add(int64(n))
	return 0, false, n
}

// mapc returns a copy of e with f applied to each child.
func mapc(e *Expr, f func(*Expr) *Expr) *Expr {
	c := *e
	if c.A != nil {
		c.A = f(c.A)
	}
	if c.B != nil {
		c.B = f(c.B)
	}
	if c.C != nil {
		c.C = f(c.C)
	}
	return &c
}

// expand is the naive reference: every let is eliminated by explicit
// substitution of its (expanded) value into the (expanded) body.
func expand(e *Expr) *Expr {
	if e.Kind == Let {
		return subst(expand(e.B), e.Name, expand(e.A))
	}
	return mapc(e, expand)
}

// subst replaces free occurrences of name in e with val, respecting
// shadowing by inner let binders.
func subst(e *Expr, name string, val *Expr) *Expr {
	switch e.Kind {
	case Var:
		if e.Name == name {
			return val
		}
		return e
	case Let:
		v := subst(e.A, name, val)
		if e.Name == name {
			return LetE(e.Name, v, e.B)
		}
		return LetE(e.Name, v, subst(e.B, name, val))
	}
	return mapc(e, func(c *Expr) *Expr { return subst(c, name, val) })
}

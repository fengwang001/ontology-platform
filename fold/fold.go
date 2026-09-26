// Package fold is a single-pass bottom-up constant folder; x/0 is kept.
package fold

import "errors"
import "ontology/ast"

var (
	ErrNilNode      = errors.New("fold: nil node in AST")
	ErrUnknownOp    = errors.New("fold: unknown operator")
	ErrTypeMismatch = errors.New("fold: operand type mismatch")
)
var litOp = map[string]func(a, b int64) (*ast.Expr, bool){
	"+": func(a, b int64) (*ast.Expr, bool) { return ast.Int(a + b), true }, "-": func(a, b int64) (*ast.Expr, bool) { return ast.Int(a - b), true },
	"*": func(a, b int64) (*ast.Expr, bool) { return ast.Int(a * b), true }, "/": func(a, b int64) (*ast.Expr, bool) {
		if b == 0 {
			return nil, false
		}
		return ast.Int(a / b), true
	},
	"<": func(a, b int64) (*ast.Expr, bool) { return ast.Bool(a < b), true }, ">": func(a, b int64) (*ast.Expr, bool) { return ast.Bool(a > b), true },
	"<=": func(a, b int64) (*ast.Expr, bool) { return ast.Bool(a <= b), true }, ">=": func(a, b int64) (*ast.Expr, bool) { return ast.Bool(a >= b), true },
	"==": func(a, b int64) (*ast.Expr, bool) { return ast.Bool(a == b), true }, "!=": func(a, b int64) (*ast.Expr, bool) { return ast.Bool(a != b), true },
}

type folder struct {
	revisits int
	seen     map[*ast.Expr]bool
}

// Fold returns a freshly allocated folded copy; root is never mutated.
// Invalid input (nil node, unknown operator, mismatched literal types)
// panics with one of the sentinel errors — judge with recover + errors.Is.
func Fold(root *ast.Expr) *ast.Expr {
	f := &folder{seen: map[*ast.Expr]bool{}}
	out, _ := f.fold(root)
	return out
}
func (f *folder) fold(e *ast.Expr) (*ast.Expr, bool) {
	if e == nil {
		panic(ErrNilNode)
	}
	if f.seen[e] {
		f.revisits++
	}
	f.seen[e] = true
	switch e.Kind {
	case ast.KInt, ast.KBool, ast.KVar:
		return ast.DeepCopy(e), false // rules 1,2: leaves are already folded
	case ast.KNeg, ast.KNot:
		return f.foldUn(e)
	case ast.KIf:
		return f.foldIf(e)
	case ast.KBin:
		return f.foldBin(e)
	}
	panic(ErrNilNode)
}
func (f *folder) foldUn(e *ast.Expr) (*ast.Expr, bool) { // rule 3
	s, d := f.fold(e.Sub)
	neg := e.Kind == ast.KNeg
	if (neg && s.Kind == ast.KBool) || (!neg && s.Kind == ast.KInt) {
		panic(ErrTypeMismatch)
	}
	if s.Kind == ast.KInt {
		return ast.Int(-s.I), false
	}
	if s.Kind == ast.KBool {
		return ast.Bool(!s.B), false
	}
	return map[bool]*ast.Expr{true: ast.Neg(s), false: ast.Not(s)}[neg], d
}
func (f *folder) foldIf(e *ast.Expr) (*ast.Expr, bool) { // rule 7
	c, _ := f.fold(e.C)
	if c.Kind == ast.KInt {
		panic(ErrTypeMismatch)
	}
	if c.Kind == ast.KBool { // unselected branch is never entered
		return f.fold(map[bool]*ast.Expr{true: e.T, false: e.E}[c.B])
	}
	t, dt := f.fold(e.T)
	r, dr := f.fold(e.E)
	return ast.If(c, t, r), dt || dr
}
func (f *folder) foldBin(e *ast.Expr) (*ast.Expr, bool) { // rules 4,5
	if e.Op == "&&" || e.Op == "||" {
		return f.foldLogic(e)
	}
	if _, ok := litOp[e.Op]; !ok {
		panic(ErrUnknownOp)
	}
	l, dl := f.fold(e.L)
	r, dr := f.fold(e.R)
	bad := dl || dr || (e.Op == "/" && isZero(r))
	if lit(l) && lit(r) {
		if l.Kind != r.Kind {
			panic(ErrTypeMismatch)
		}
		if l.Kind == ast.KBool {
			if e.Op != "==" && e.Op != "!=" {
				panic(ErrTypeMismatch)
			}
			return ast.Bool((l.B == r.B) == (e.Op == "==")), false
		}
		if v, ok := litOp[e.Op](l.I, r.I); ok { // /0 -> ok=false: keep node
			return v, false
		}
	}
	if v, db, ok := identity(e.Op, l, r, dl, dr); ok {
		return v, db
	}
	return ast.Bin(e.Op, l, r), bad
}
func (f *folder) foldLogic(e *ast.Expr) (*ast.Expr, bool) { // rule 6
	l, dl := f.fold(e.L)
	if (e.Op == "&&" && boolLit(l, false)) || (e.Op == "||" && boolLit(l, true)) {
		return ast.Bool(e.Op == "||"), false // RHS is never entered
	}
	r, dr := f.fold(e.R)
	if l.Kind == ast.KInt {
		panic(ErrTypeMismatch)
	}
	if l.Kind == ast.KBool {
		return r, dr // true && e / false || e -> e
	}
	return ast.Bin(e.Op, l, r), dl || dr
}

func identity(op string, l, r *ast.Expr, dl, dr bool) (*ast.Expr, bool, bool) { // rule 5: guard only when a non-literal operand is erased.
	switch {
	case (op == "+" || op == "-") && isZero(r), op == "*" && isOne(r), op == "/" && isOne(r):
		return l, dl, true
	case op == "+" && isZero(l), op == "*" && isOne(l):
		return r, dr, true
	case op == "*" && ((isZero(l) && !dr) || (isZero(r) && !dl)):
		return ast.Int(0), false, true
	}
	return nil, false, false
}
func isZero(e *ast.Expr) bool          { return e.Kind == ast.KInt && e.I == 0 }
func isOne(e *ast.Expr) bool           { return e.Kind == ast.KInt && e.I == 1 }
func lit(e *ast.Expr) bool             { return e.Kind == ast.KInt || e.Kind == ast.KBool }
func boolLit(e *ast.Expr, b bool) bool { return e.Kind == ast.KBool && e.B == b }

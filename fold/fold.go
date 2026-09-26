// Package fold implements single-pass bottom-up constant folding.
package fold

import "ontology/ast"

// Fold returns a folded copy of n without modifying it; it never fails.
func Fold(n *ast.Expr) *ast.Expr {
	f := &folder{seen: make(map[*ast.Expr]bool)}
	return f.fold(n)
}

// folder is per-call state; revisits stays 0 for single-pass folding.
type folder struct {
	seen     map[*ast.Expr]bool
	revisits int
}

func (f *folder) fold(n *ast.Expr) *ast.Expr {
	if n == nil {
		return nil
	}
	if f.seen[n] {
		f.revisits++
	}
	f.seen[n] = true
	switch n.Kind {
	case ast.KNeg:
		l := f.fold(n.L)
		if l.Kind == ast.KInt {
			return ast.Int(-l.Int)
		}
		return ast.Neg(l)
	case ast.KNot:
		l := f.fold(n.L)
		if l.Kind == ast.KBool {
			return ast.Bool(!l.Bool)
		}
		return ast.Not(l)
	case ast.KBin:
		return f.bin(n)
	case ast.KIf:
		c := f.fold(n.C)
		if c.Kind == ast.KBool {
			if c.Bool {
				return f.fold(n.T)
			}
			return f.fold(n.E)
		}
		return ast.If(c, f.fold(n.T), f.fold(n.E))
	default: // KInt, KBool, KVar: already folded
		return ast.Copy(n)
	}
}
func (f *folder) bin(n *ast.Expr) *ast.Expr {
	l := f.fold(n.L)
	// Rule 6: short-circuit; a decided left side means the right side is
	// never folded (false&&e, true||e) or becomes the result (true&&e, false||e).
	if n.Op == "&&" || n.Op == "||" {
		if l.Kind == ast.KBool {
			if (n.Op == "&&") != l.Bool {
				return ast.Bool(l.Bool)
			}
			return f.fold(n.R)
		}
		return ast.Bin(n.Op, l, f.fold(n.R))
	}
	r := f.fold(n.R)
	if l.Kind == ast.KInt && r.Kind == ast.KInt { // rule 4
		if v, ok := arith(n.Op, l.Int, r.Int); ok {
			return v
		}
	}
	if l.Kind == ast.KBool && r.Kind == ast.KBool && (n.Op == "==" || n.Op == "!=") {
		return ast.Bool((l.Bool == r.Bool) == (n.Op == "=="))
	}
	if v, ok := identity(n.Op, l, r); ok { // rule 5
		return v
	}
	return ast.Bin(n.Op, l, r)
}

var (
	intOps = map[string]func(int64, int64) int64{
		"+": func(a, b int64) int64 { return a + b }, "-": func(a, b int64) int64 { return a - b },
		"*": func(a, b int64) int64 { return a * b }, "/": func(a, b int64) int64 { return a / b },
	}
	cmpOps = map[string]func(int64, int64) bool{
		"<": func(a, b int64) bool { return a < b }, ">": func(a, b int64) bool { return a > b },
		"<=": func(a, b int64) bool { return a <= b }, ">=": func(a, b int64) bool { return a >= b },
		"==": func(a, b int64) bool { return a == b }, "!=": func(a, b int64) bool { return a != b },
	}
)

// arith evaluates int-int ops; "/0" reports not-ok (rule 4 exception).
func arith(op string, a, b int64) (*ast.Expr, bool) {
	if op == "/" && b == 0 {
		return nil, false
	}
	if fn, ok := intOps[op]; ok {
		return ast.Int(fn(a, b)), true
	}
	if fn, ok := cmpOps[op]; ok {
		return ast.Bool(fn(a, b)), true
	}
	return nil, false
}

// idRule: op with int literal `lit` on side `right` folds to the other side
// e — or, for zero=true, to 0, but only if e is pure (rule 5).
var idRules = []struct {
	op    string
	lit   int64
	right bool
	zero  bool
}{
	{"+", 0, true, false}, {"+", 0, false, false}, {"-", 0, true, false},
	{"*", 1, true, false}, {"*", 1, false, false}, {"/", 1, true, false},
	{"*", 0, true, true}, {"*", 0, false, true},
}

func identity(op string, l, r *ast.Expr) (*ast.Expr, bool) {
	for _, ru := range idRules {
		e, lit := l, r
		if !ru.right {
			e, lit = r, l
		}
		if ru.op != op || lit.Kind != ast.KInt || lit.Int != ru.lit {
			continue
		}
		if !ru.zero {
			return e, true
		}
		if !mayDivZero(e) {
			return ast.Int(0), true
		}
	}
	return nil, false
}

// mayDivZero: could e raise a division by zero under some substitution?
// Any "/" whose right operand is not a nonzero int literal counts.
func mayDivZero(e *ast.Expr) bool {
	if e == nil {
		return false
	}
	if e.Kind == ast.KBin && e.Op == "/" && (e.R.Kind != ast.KInt || e.R.Int == 0) {
		return true
	}
	return mayDivZero(e.L) || mayDivZero(e.R) || mayDivZero(e.C) || mayDivZero(e.T) || mayDivZero(e.E)
}

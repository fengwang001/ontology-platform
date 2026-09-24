// Package fold implements constant-folding rewrite rules. All rules are
// exact three-valued equivalences except FoldTop, which is only valid for
// top-level conjuncts (WHERE filtering semantics).
package fold

import "ontology/ast"

// Rule rewrites a single node; it reports whether anything changed.
type Rule struct {
	Name  string
	Apply func(*ast.Node) (*ast.Node, bool)
}

func isConst(n *ast.Node, v ast.Value) bool { return n.Kind == ast.KConst && n.Val == v }

func constEval(n *ast.Node) (*ast.Node, bool) {
	if n.Kind == ast.KConst || n.Kind == ast.KColumn {
		return n, false
	}
	for _, k := range n.Kids {
		if k.Kind != ast.KConst {
			return n, false
		}
	}
	return ast.Const(n.Eval(nil)), true
}

// hitConst fires when any kid is the absorbing constant (AND/False, OR/True).
func hitConst(kind ast.Kind, hit ast.Value) func(*ast.Node) (*ast.Node, bool) {
	return func(n *ast.Node) (*ast.Node, bool) {
		if n.Kind != kind {
			return n, false
		}
		for _, k := range n.Kids {
			if isConst(k, hit) {
				return ast.Const(hit), true
			}
		}
		return n, false
	}
}

// dropConst removes identity constants (AND/True, OR/False) from kids.
func dropConst(kind ast.Kind, drop ast.Value) func(*ast.Node) (*ast.Node, bool) {
	return func(n *ast.Node) (*ast.Node, bool) {
		if n.Kind != kind {
			return n, false
		}
		out := n.Kids[:0]
		changed := false
		for _, k := range n.Kids {
			if isConst(k, drop) {
				changed = true
				continue
			}
			out = append(out, k)
		}
		if !changed {
			return n, false
		}
		if len(out) == 0 {
			return ast.Const(drop), true
		}
		return &ast.Node{Kind: kind, Kids: out}, true
	}
}

func singleKid(n *ast.Node) (*ast.Node, bool) {
	if (n.Kind == ast.KAnd || n.Kind == ast.KOr) && len(n.Kids) == 1 {
		return n.Kids[0], true
	}
	return n, false
}

func notNot(n *ast.Node) (*ast.Node, bool) {
	if n.Kind == ast.KNot && n.Kids[0].Kind == ast.KNot {
		return n.Kids[0].Kids[0], true
	}
	return n, false
}

func deMorgan(from, to ast.Kind) func(*ast.Node) (*ast.Node, bool) {
	return func(n *ast.Node) (*ast.Node, bool) {
		if n.Kind != ast.KNot || n.Kids[0].Kind != from {
			return n, false
		}
		kids := make([]*ast.Node, len(n.Kids[0].Kids))
		for i, k := range n.Kids[0].Kids {
			kids[i] = ast.Not(k)
		}
		return &ast.Node{Kind: to, Kids: kids}, true
	}
}

func dupKid(n *ast.Node) (*ast.Node, bool) {
	if n.Kind != ast.KAnd && n.Kind != ast.KOr {
		return n, false
	}
	seen := map[string]bool{}
	out := n.Kids[:0]
	changed := false
	for _, k := range n.Kids {
		if s := k.String(); seen[s] {
			changed = true
			continue
		} else {
			seen[s] = true
		}
		out = append(out, k)
	}
	if !changed {
		return n, false
	}
	return &ast.Node{Kind: n.Kind, Kids: out}, true
}

// Rules returns the position-independent rules (each is an exact
// three-valued equivalence, verified exhaustively by package equiv).
func Rules() []Rule {
	return []Rule{
		{"and-false", hitConst(ast.KAnd, ast.False)},
		{"and-true", dropConst(ast.KAnd, ast.True)},
		{"const-eval", constEval},
		{"de-morgan-and", deMorgan(ast.KAnd, ast.KOr)},
		{"de-morgan-or", deMorgan(ast.KOr, ast.KAnd)},
		{"dup-kid", dupKid},
		{"not-not", notNot},
		{"or-false", dropConst(ast.KOr, ast.False)},
		{"or-true", hitConst(ast.KOr, ast.True)},
		{"single-kid", singleKid},
	}
}

// FoldTop applies the "contradiction" rule (A AND NOT A -> false), which is
// only filter-equivalent, to the top-level AND chain: the root and any AND
// node reachable from it through ANDs only. Anything under NOT/OR is left
// untouched. It reports whether anything changed.
func FoldTop(n *ast.Node) (*ast.Node, bool) {
	if n.Kind != ast.KAnd {
		return n, false
	}
	changed := false
	kids := make([]*ast.Node, len(n.Kids))
	for i, k := range n.Kids {
		var c bool
		kids[i], c = FoldTop(k)
		changed = changed || c
	}
	neg := map[string]bool{}
	for _, k := range kids {
		if k.Kind == ast.KNot {
			neg[k.Kids[0].String()] = true
		}
	}
	for _, k := range kids {
		if k.Kind != ast.KNot && neg[k.String()] {
			return ast.Const(ast.False), true
		}
	}
	if !changed {
		return n, false
	}
	return &ast.Node{Kind: ast.KAnd, Kids: kids}, true
}

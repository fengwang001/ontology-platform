package fold

import (
	"testing"

	"ontology/ast"
)

// countNodes walks an AST and counts its nodes.
func countNodes(n *ast.Expr) int {
	if n == nil {
		return 0
	}
	switch n.Kind {
	case ast.KInt, ast.KBool, ast.KVar:
		return 1
	case ast.KNeg, ast.KNot:
		return 1 + countNodes(n.Sub)
	case ast.KBin:
		return 1 + countNodes(n.L) + countNodes(n.R)
	case ast.KIf:
		return 1 + countNodes(n.C) + countNodes(n.T) + countNodes(n.E)
	}
	return 0
}

// sumTree builds a left-deep tree 1+1+...+1 with m constant leaves.
func sumTree(m int) *ast.Expr {
	n := ast.Int(1)
	for i := 1; i < m; i++ {
		n = ast.Bin("+", n, ast.Int(1))
	}
	return n
}

// TestSinglePass pins the single-pass complexity claim. Over several m in
// [100,10000], a fresh folder must enter every node at most once, so the
// unexported revisits counter stays 0 (no fixed-point re-scanning).
func TestSinglePass(t *testing.T) {
	for _, m := range []int{100, 1000, 5000, 10000} {
		root := sumTree(m)
		before := countNodes(root)
		f := &folder{seen: map[*ast.Expr]bool{}}
		out, _ := f.fold(root)
		if f.revisits != 0 {
			t.Fatalf("m=%d: %d nodes re-entered, want 0 (single pass)", m, f.revisits)
		}
		if out.Kind != ast.KInt || out.I != int64(m) {
			t.Fatalf("m=%d: got %s, want Int %d", m, out, m)
		}
		if got := countNodes(root); got != before { // input untouched
			t.Fatalf("m=%d: input mutated, nodes %d -> %d", m, before, got)
		}
	}
}

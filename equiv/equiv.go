// Package equiv verifies equivalence of two predicate trees by exhaustive
// enumeration of all 3^n assignments over their columns.
package equiv

import (
	"fmt"
	"strings"

	"ontology/ast"
)

// compare enumerates every assignment; filter=true compares only whether the
// result is True (WHERE semantics), otherwise compares exact truth values.
// On mismatch it returns a counterexample assignment.
func compare(a, b *ast.Node, filter bool) (bool, string) {
	cols := ast.Columns(a, b)
	if len(cols) > 12 {
		return false, fmt.Sprintf("too many columns: %d", len(cols))
	}
	total := 1
	for range cols {
		total *= 3
	}
	env := make(map[string]ast.Value, len(cols))
	for i := 0; i < total; i++ {
		n := i
		var sb strings.Builder
		for j, c := range cols {
			v := ast.Value(n % 3)
			n /= 3
			env[c] = v
			if j > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(c + "=" + v.String())
		}
		va, vb := a.Eval(env), b.Eval(env)
		same := va == vb
		if filter {
			same = (va == ast.True) == (vb == ast.True)
		}
		if !same {
			return false, fmt.Sprintf("%s: left=%s right=%s", sb.String(), va, vb)
		}
	}
	return true, ""
}

// Equal reports whether a and b agree on every assignment, value for value.
func Equal(a, b *ast.Node) (bool, string) { return compare(a, b, false) }

// FilterEqual reports whether a and b keep the same rows in a WHERE clause
// (only the "is True" bit must agree; False and Unknown are identified).
func FilterEqual(a, b *ast.Node) (bool, string) { return compare(a, b, true) }

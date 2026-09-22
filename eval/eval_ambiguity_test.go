package eval

import (
	"testing"
)

func TestParseUniqueness(t *testing.T) {
	cases := []struct {
		expr   string
		tree   string
		result any
	}{
		// Same precedence is strictly left-associative.
		{"1-2-3", "((1-2)-3)", int64(-4)},
		{"8/4/2", "((8/4)/2)", int64(1)},
		{"1-2+3", "((1-2)+3)", int64(2)},
		// Multiplication and division bind tighter than plus and minus.
		{"2+3*4", "(2+(3*4))", int64(14)},
		{"2*3+4", "((2*3)+4)", int64(10)},
		{"2+6/3", "(2+(6/3))", int64(4)},
		// Unary minus binds tighter than '*' and '/'.
		{"-2*3", "((-2)*3)", int64(-6)},
		{"-2*-3", "((-2)*(-3))", int64(6)},
		{"1/-2", "(1/(-2))", float64(-0.5)},
		{"3*-2+4", "((3*(-2))+4)", int64(-2)},
		// Repeated unary minus.
		{"--3", "(-(-3))", int64(3)},
		{"---3", "(-(-(-3)))", int64(-3)},
		// Unary minus combined with parentheses.
		{"-(-2)", "(-(-2))", int64(2)},
		{"-(2+3)", "(-(2+3))", int64(-5)},
		// Unary minus is fine as a binary right operand.
		{"1+-2", "(1+(-2))", int64(-1)},
		{"1--2", "(1-(-2))", int64(3)},
		{"3--2*4", "(3-((-2)*4))", int64(11)},
		// Parentheses are the highest precedence and add no node.
		{"(1+2)*3", "((1+2)*3)", int64(9)},
		{"((1))", "1", int64(1)},
		// Whitespace at any depth does not change the tree.
		{"  1 - 2 - 3 ", "((1-2)-3)", int64(-4)},
		{"\t2\n+\t3 *4", "(2+(3*4))", int64(14)},
		// Exact division giving a float64 result.
		{"2*3/4", "((2*3)/4)", float64(1.5)},
		{"1/4", "(1/4)", float64(0.25)},
	}
	for _, tc := range cases {
		t.Run(tc.expr, func(t *testing.T) {
			value, root, err := Eval(tc.expr)
			if err != nil {
				t.Fatalf("Eval(%q): %v", tc.expr, err)
			}
			if got := root.String(); got != tc.tree {
				t.Fatalf("tree = %q, want %q", got, tc.tree)
			}
			if !valueEqual(value, tc.result) {
				t.Fatalf("value = %v (%T), want %v (%T)",
					value, value, tc.result, tc.result)
			}
		})
	}
}

func valueEqual(got, want any) bool {
	switch w := want.(type) {
	case int64:
		g, ok := got.(int64)
		return ok && g == w
	case float64:
		g, ok := got.(float64)
		return ok && g == w
	default:
		return got == want
	}
}

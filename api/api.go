// Package api is the public facade: parse and evaluate expression strings.
package api

import (
	"errors"

	"ontology/lex"
	"ontology/pratt"
)

// Parse lexes and parses s into an AST.
func Parse(s string) (*pratt.Node, error) {
	toks, err := lex.Lex(s)
	if err != nil {
		return nil, err
	}
	return pratt.Parse(toks)
}

// Eval parses and evaluates s.
func Eval(s string) (int64, error) {
	n, err := Parse(s)
	if err != nil {
		return 0, err
	}
	return pratt.Eval(n)
}

// EvalNode evaluates an AST produced by Parse.
func EvalNode(n *pratt.Node) (int64, error) {
	if n == nil {
		return 0, pratt.ErrSyntax
	}
	return pratt.Eval(n)
}

// SelfCheck verifies the four invariants from NOTES.md against a built-in
// expression set: agreement with a naive fully-parenthesized reference,
// right-associativity of ^, prefix/infix minus disambiguation, and that
// rejected inputs fail with distinct sentinel errors. It also runs the
// pratt early-stop check. It returns nil iff everything holds.
func SelfCheck() error {
	cases := []struct {
		expr string
		want int64
	}{
		{"2^3^2", 512},    // ^ is right-associative
		{"2^(3^2)", 512},  // naive parenthesized reference agrees
		{"(2^3)^2", 64},   // explicit left-nesting differs
		{"10-4-3", 3},     // - is left-associative
		{"10-(4-3)", 9},   // reference for the line above
		{"-2^2", 4},       // prefix minus binds tighter than ^
		{"(-2)^2", 4},     // reference for the line above
		{"-(2^2)", -4},    // the other nesting differs
		{"- -3", 3},       // stacked prefix minus
		{"5- -3", 8},      // infix minus followed by prefix minus
		{"7/2", 3},        // truncation toward zero
		{"(0-7)/2", -3},   // -7/2 truncates toward zero, not down
		{"2^0", 1},        // a^0 = 1
		{"0^0", 1},        // 0^0 = 1
		{"2*(3+4)-1", 13}, // parentheses override precedence
		{"(1+2)^2", 9},    // parentheses around a sum as base
		{"9223372036854775807", 9223372036854775807}, // max int64 literal
	}
	for _, c := range cases {
		got, err := Eval(c.expr)
		if err != nil || got != c.want {
			return errSelfCheck
		}
	}
	// Rejected inputs: four distinct sentinel classes, all different.
	rejects := []struct {
		expr string
		sent error
	}{
		{"1&2", lex.ErrIllegalChar},
		{"(1+2", pratt.ErrParens},
		{"2^(0-1)", pratt.ErrExponent},
		{"1/0", pratt.ErrDivZero},
	}
	sents := map[error]bool{}
	for _, r := range rejects {
		if _, err := Eval(r.expr); !errors.Is(err, r.sent) {
			return errSelfCheck
		}
		if sents[r.sent] {
			return errSelfCheck // sentinels must be mutually distinct
		}
		sents[r.sent] = true
	}
	// A rejection must not corrupt later calls.
	if v, err := Eval("2+3"); err != nil || v != 5 {
		return errSelfCheck
	}
	return pratt.SelfCheck()
}

var errSelfCheck = errors.New("api: self-check failed")

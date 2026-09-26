// Package api is the public facade over codec.
package api

import (
	"fmt"

	"ontology/ast"
	"ontology/codec"
)

// EncodeExpr serializes n into the wire format.
func EncodeExpr(n *ast.Expr) []byte { return codec.Encode(n) }

// DecodeExpr parses one expression from b.
func DecodeExpr(b []byte) (*ast.Expr, error) { return codec.Decode(b) }

// RoundTrip encodes n and decodes it back.
func RoundTrip(n *ast.Expr) (*ast.Expr, error) { return codec.Decode(codec.Encode(n)) }

// SelfCheck verifies the four invariants on a built-in set of ASTs.
// It returns the first violation found, or nil.
func SelfCheck() error {
	cases := []*ast.Expr{
		ast.Int(0), ast.Int(-1), ast.Int(4294967296), ast.Int(-9223372036854775808),
		ast.Bool(true), ast.Bool(false),
		ast.Name(""), ast.Name("a\x00b"), ast.Name("\xff\xfe"), ast.Name("x"),
		ast.NegOf(ast.Int(5)), ast.NotOf(ast.Bool(true)),
		ast.Bin(ast.OpAdd, ast.Int(258), ast.Name("ab")),
		ast.Bin(ast.OpOr, ast.Bin(ast.OpLe, ast.Name("x"), ast.Int(1)), ast.NotOf(ast.Name("y"))),
		ast.IfElse(ast.Bin(ast.OpEq, ast.Name("k"), ast.Int(0)),
			ast.NegOf(ast.Int(-9)), ast.Bin(ast.OpDiv, ast.Int(1), ast.Int(2))),
	}
	for i, n := range cases {
		// Invariant 1+2: round-trip is structurally equal, byte count stable.
		rt, err := RoundTrip(n)
		if err != nil {
			return fmt.Errorf("selfcheck case %d: %w", i, err)
		}
		if !ast.Equal(rt, n) {
			return fmt.Errorf("selfcheck case %d: round-trip not equal", i)
		}
		if got, want := len(codec.Encode(rt)), len(codec.Encode(n)); got != want {
			return fmt.Errorf("selfcheck case %d: re-encode width %d != %d", i, got, want)
		}
	}
	// Invariant 4: rejected inputs yield no AST and leave no residue.
	bad := [][]byte{
		codec.Encode(ast.Int(1))[:4], // truncated
		{7},                          // bad tag
		{1, 3},                       // bool byte not 0/1
		{2, 9, 0, 0, 0, 'a'},         // var length exceeds remainder
	}
	for i, b := range bad {
		if n, err := DecodeExpr(b); err == nil || n != nil {
			return fmt.Errorf("selfcheck bad input %d: accepted", i)
		}
	}
	if _, err := RoundTrip(cases[0]); err != nil {
		return fmt.Errorf("selfcheck: state polluted after rejections: %w", err)
	}
	return nil
}

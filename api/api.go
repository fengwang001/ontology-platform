// Package api is the public entry point of the type checker.
package api

import (
	"errors"
	"fmt"

	"ontology/check"
	"ontology/types"
)

// Check infers the type of e starting from the empty environment.
func Check(e *check.Expr) (types.T, error) {
	return check.Infer(e, check.NewEnv())
}

// SelfCheck verifies the four invariants on a built-in battery of
// expressions: agreement with the naive substitution reference, let
// scoping, if-branch consistency, and failure leaving no trace.
func SelfCheck() error {
	good := []struct {
		e *check.Expr
		t types.T
	}{
		{check.LetE("x", check.IntL(3), check.IfE(check.BinE("<", check.VarE("x"), check.IntL(5)),
			check.BinE("+", check.VarE("x"), check.IntL(1)), check.IntL(0))), types.Int},
		{check.BinE("&&", check.BoolL(true), check.BinE("<", check.IntL(1), check.IntL(2))), types.Bool},
		{check.BinE("==", check.IntL(1), check.IntL(1)), types.Bool},
		{check.LetE("x", check.IntL(1), check.LetE("x", check.BoolL(true), check.VarE("x"))), types.Bool},
	}
	bad := []struct {
		e   *check.Expr
		err error
	}{
		{check.BinE("+", check.VarE("z"), check.IntL(1)), check.ErrUndeclared},
		{check.BinE("+", check.LetE("x", check.IntL(1), check.VarE("x")), check.VarE("x")), check.ErrUndeclared},
		{check.BinE("<", check.IntL(1), check.BoolL(true)), check.ErrArithOperand},
		{check.BinE("&&", check.IntL(1), check.BoolL(true)), check.ErrLogicOperand},
		{check.IfE(check.BoolL(true), check.IntL(1), check.BoolL(true)), check.ErrIf},
		{check.IfE(check.IntL(1), check.IntL(1), check.IntL(2)), check.ErrIf},
		{check.BinE("==", check.IntL(1), check.BoolL(true)), check.ErrEqMismatch},
	}
	for i, c := range good {
		t, err := Check(c.e)
		if err != nil || t != c.t {
			return fmt.Errorf("selfcheck good[%d]: got %v,%v want %s", i, t, err, c.t)
		}
		if nt, nerr := naive(c.e); nerr != nil || nt != t { // invariant 1
			return fmt.Errorf("selfcheck naive[%d]: %v,%v vs %s", i, nt, nerr, t)
		}
	}
	for i, c := range bad {
		if t, err := Check(c.e); !errors.Is(err, c.err) {
			return fmt.Errorf("selfcheck bad[%d]: got %v,%v want %v", i, t, err, c.err)
		}
	}
	// Invariant 4: after rejected checks, a valid check still succeeds.
	if t, err := Check(good[0].e); err != nil || t != types.Int {
		return fmt.Errorf("selfcheck: state leaked after rejection: %v,%v", t, err)
	}
	return nil
}

// naive is the reference: expand every let by explicit substitution, then
// re-infer node by node from the empty environment.
func naive(e *check.Expr) (types.T, error) {
	return check.Infer(expand(e), check.NewEnv())
}

func expand(e *check.Expr) *check.Expr {
	if e.Kind == check.Let {
		return subst(expand(e.B), e.Name, expand(e.A))
	}
	c := *e
	if c.A != nil {
		c.A = expand(c.A)
	}
	if c.B != nil {
		c.B = expand(c.B)
	}
	if c.C != nil {
		c.C = expand(c.C)
	}
	return &c
}

func subst(e *check.Expr, name string, val *check.Expr) *check.Expr {
	switch e.Kind {
	case check.Var:
		if e.Name == name {
			return val
		}
		return e
	case check.Let:
		v := subst(e.A, name, val)
		if e.Name == name { // inner binder shadows; body untouched
			return check.LetE(e.Name, v, e.B)
		}
		return check.LetE(e.Name, v, subst(e.B, name, val))
	}
	c := *e
	if c.A != nil {
		c.A = subst(c.A, name, val)
	}
	if c.B != nil {
		c.B = subst(c.B, name, val)
	}
	if c.C != nil {
		c.C = subst(c.C, name, val)
	}
	return &c
}

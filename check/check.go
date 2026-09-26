// Package check implements the type checker: scoped environments and
// type inference over the expression AST.
package check

import (
	"errors"
	"fmt"

	"ontology/types"
)

// Decidable, mutually distinct type-error categories.
var (
	ErrUndeclared   = errors.New("undeclared variable")
	ErrArithOperand = errors.New("arithmetic/comparison operand is not int")
	ErrLogicOperand = errors.New("logic operand is not bool")
	ErrIf           = errors.New("if condition not bool or branch types differ")
	ErrEqMismatch   = errors.New("equality operands have different types")
)

// Infer infers the type of e under env, or returns a decidable error.
// It never mutates env or any global state.
func Infer(e *Expr, env *Env) (types.T, error) {
	switch e.Kind {
	case IntLit:
		return types.Int, nil
	case BoolLit:
		return types.Bool, nil
	case Var:
		if t, ok := env.Lookup(e.Name); ok {
			return t, nil
		}
		return 0, fmt.Errorf("%w: %s", ErrUndeclared, e.Name)
	case Neg:
		return unary(e, env, types.Int, ErrArithOperand)
	case Not:
		return unary(e, env, types.Bool, ErrLogicOperand)
	case Bin:
		return inferBin(e, env)
	case If:
		return inferIf(e, env)
	case Let:
		t, err := Infer(e.A, env)
		if err != nil {
			return 0, err
		}
		return Infer(e.B, env.Extend(e.Name, t))
	}
	return 0, fmt.Errorf("bad node kind %d", e.Kind)
}

// unary checks e.A against want and returns want.
func unary(e *Expr, env *Env, want types.T, kind error) (types.T, error) {
	t, err := Infer(e.A, env)
	if err != nil {
		return 0, err
	}
	if t != want {
		return 0, fmt.Errorf("%w: got %s", kind, t)
	}
	return want, nil
}

func inferIf(e *Expr, env *Env) (types.T, error) {
	c, err := Infer(e.A, env)
	if err != nil {
		return 0, err
	}
	if c != types.Bool {
		return 0, fmt.Errorf("%w: condition is %s", ErrIf, c)
	}
	t, err := Infer(e.B, env)
	if err != nil {
		return 0, err
	}
	f, err := Infer(e.C, env)
	if err != nil {
		return 0, err
	}
	if t != f {
		return 0, fmt.Errorf("%w: %s vs %s", ErrIf, t, f)
	}
	return t, nil
}

func inferBin(e *Expr, env *Env) (types.T, error) {
	l, err := Infer(e.A, env)
	if err != nil {
		return 0, err
	}
	r, err := Infer(e.B, env)
	if err != nil {
		return 0, err
	}
	switch e.Op {
	case "+", "-", "*", "/", "<", ">", "<=", ">=":
		if l != types.Int || r != types.Int {
			return 0, fmt.Errorf("%w: %s %s %s", ErrArithOperand, l, e.Op, r)
		}
		if e.Op == "+" || e.Op == "-" || e.Op == "*" || e.Op == "/" {
			return types.Int, nil
		}
		return types.Bool, nil
	case "==", "!=":
		if l != r {
			return 0, fmt.Errorf("%w: %s %s %s", ErrEqMismatch, l, e.Op, r)
		}
		return types.Bool, nil
	case "&&", "||":
		if l != types.Bool || r != types.Bool {
			return 0, fmt.Errorf("%w: %s %s %s", ErrLogicOperand, l, e.Op, r)
		}
		return types.Bool, nil
	}
	return 0, fmt.Errorf("unknown operator %q", e.Op)
}

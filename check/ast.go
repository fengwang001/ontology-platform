// Package check holds the expression AST, scoped Env and Infer; depends only on ontology/types.
package check

import (
	"errors"
	"fmt"
	"maps"

	"ontology/types"
)

// Expr is one parsed expression node; Kids holds 0..3 sub-expressions.
type Expr struct {
	Kind Kind
	Op   string
	Int  int64
	Bool bool
	Name string
	Kids [3]*Expr
}

type Kind uint8

const (
	KIntLit Kind = iota
	KBoolLit
	KVar
	KNeg
	KNot
	KArith
	KCmp
	KEq
	KLogic
	KIf
	KLet
)

func L(n int64) *Expr        { return &Expr{Kind: KIntLit, Int: n} }
func B(v bool) *Expr         { return &Expr{Kind: KBoolLit, Bool: v} }
func V(n string) *Expr       { return &Expr{Kind: KVar, Name: n} }
func If(c, t, e *Expr) *Expr { return &Expr{Kind: KIf, Kids: [3]*Expr{c, t, e}} }
func Let(n string, v, b *Expr) *Expr {
	return &Expr{Kind: KLet, Name: n, Kids: [3]*Expr{v, b}}
}

func U(op string, a *Expr) *Expr {
	k := KNot
	if op == "-" {
		k = KNeg
	}
	return &Expr{Kind: k, Op: op, Kids: [3]*Expr{a}}
}

func Bin(op string, a, b *Expr) *Expr {
	k := KArith
	switch op {
	case "<", ">", "<=", ">=":
		k = KCmp
	case "==", "!=":
		k = KEq
	case "&&", "||":
		k = KLogic
	}
	return &Expr{Kind: k, Op: op, Kids: [3]*Expr{a, b}}
}

// Decidable sentinel errors; the failure classes are pairwise distinct.
var (
	ErrUndefined    = errors.New("typecheck: undefined variable")
	ErrIntRequired  = errors.New("typecheck: arithmetic/comparison needs int operands")
	ErrBoolRequired = errors.New("typecheck: logical operator needs bool operands")
	ErrIf           = errors.New("typecheck: if needs bool condition and matching branches")
	ErrEqMismatch   = errors.New("typecheck: == and != need operands of the same type")
)

type naiveLet struct {
	e   *Expr
	env map[string]*naiveLet
}

// NaiveInfer is the independent reference: it re-infers every node and expands each let by call-by-name substitution, sharing visit order and the rule table with Infer so error classes match, sharing no env/cache.
func NaiveInfer(e *Expr, base map[string]types.T) (types.T, error) {
	m := map[string]*naiveLet{}
	for n, t := range base {
		l := L(0)
		if !t.Equal(types.Int) {
			l = B(false)
		}
		m[n] = &naiveLet{l, nil}
	}
	return naiveRef(e, m)
}

func nkids(e *Expr, m map[string]*naiveLet) (ts [3]types.T, err error) {
	n := 2
	switch e.Kind {
	case KIf:
		n = 3
	case KNeg, KNot:
		n = 1
	}
	for i := 0; i < n; i++ {
		if ts[i], err = naiveRef(e.Kids[i], m); err != nil {
			return
		}
	}
	return
}

func naiveRef(e *Expr, env map[string]*naiveLet) (types.T, error) {
	switch e.Kind {
	case KIntLit:
		return types.Int, nil
	case KBoolLit:
		return types.Bool, nil
	case KVar:
		c, ok := env[e.Name]
		if !ok {
			return types.T{}, fmt.Errorf("%w: %s", ErrUndefined, e.Name)
		}
		return naiveRef(c.e, c.env)
	case KNeg, KNot, KArith, KCmp, KLogic, KEq:
		ts, err := nkids(e, env)
		if err != nil {
			return types.T{}, err
		}
		if e.Kind == KNeg || e.Kind == KNot {
			return unaryRule(e.Kind, e.Op, ts[0])
		}
		return binaryRule(e.Kind, e.Op, ts[0], ts[1])
	case KIf:
		ts, err := nkids(e, env)
		if err != nil {
			return types.T{}, err
		}
		if !ts[0].Equal(types.Bool) || !ts[1].Equal(ts[2]) {
			return types.T{}, fmt.Errorf("%w: cond=%s then=%s else=%s", ErrIf, ts[0], ts[1], ts[2])
		}
		return ts[1], nil
	case KLet:
		if _, err := naiveRef(e.Kids[0], env); err != nil {
			return types.T{}, err
		}
		m2 := maps.Clone(env)
		m2[e.Name] = &naiveLet{e.Kids[0], env}
		return naiveRef(e.Kids[1], m2)
	}
	return types.T{}, errors.New("check: unknown node")
}

// Package api is the public entry point: Check from an empty environment and
// SelfCheck over a built-in expression set; it depends only on check.
package api

import (
	"errors"
	"fmt"

	"ontology/check"
	"ontology/types"
)

// Check type-checks e starting from an empty environment.
func Check(e *check.Expr) (types.T, error) { return check.Infer(e, check.NewEnv()) }

type caseT struct {
	e    *check.Expr
	want types.T
	sent error
}

func cases() []caseT {
	return []caseT{
		{check.Let("x", check.L(3), check.If(check.Bin("<", check.V("x"), check.L(5)),
			check.Bin("+", check.V("x"), check.L(1)), check.L(0))), types.Int, nil},
		{check.If(check.B(true), check.L(1), check.B(true)), types.T{}, check.ErrIf},
		{check.Bin("<", check.L(1), check.B(true)), types.T{}, check.ErrIntRequired},
		{check.Bin("+", check.V("z"), check.L(1)), types.T{}, check.ErrUndefined},
		{check.Bin("==", check.L(1), check.B(true)), types.T{}, check.ErrEqMismatch},
		{check.Bin("&&", check.B(true), check.Bin("<", check.L(1), check.L(2))), types.Bool, nil},
		{check.Bin("==", check.L(1), check.L(1)), types.Bool, nil},
		{check.Let("x", check.L(1), check.V("x")), types.Int, nil},
		{check.V("x"), types.T{}, check.ErrUndefined},
	}
}

// SelfCheck verifies the four invariants on a built-in expression set.
func SelfCheck() error {
	for i, c := range cases() {
		got, err := Check(c.e)
		if c.sent != nil {
			if !errors.Is(err, c.sent) {
				return fmt.Errorf("case %d: want %v got %v", i, c.sent, err)
			}
			// Invariant 1: error class must equal the naive reference.
			if _, rerr := check.NaiveInfer(c.e, nil); !errors.Is(rerr, c.sent) {
				return fmt.Errorf("case %d: naive error %v != %v", i, rerr, c.sent)
			}
			continue
		}
		if err != nil || !got.Equal(c.want) {
			return fmt.Errorf("case %d: want %s got %s err %v", i, c.want, got, err)
		}
		// Invariant 1: type must equal the naive reference re-inference.
		rt, rerr := check.NaiveInfer(c.e, nil)
		if rerr != nil || !rt.Equal(got) {
			return fmt.Errorf("case %d: Check %s != naive %s/%v", i, got, rt, rerr)
		}
	}
	// Invariant 4: a rejected check must not mutate the caller environment.
	env := check.NewEnv()
	env.Bind("y", types.Int)
	if _, err := check.Infer(check.Bin("+", check.V("z"), check.L(1)), env); err == nil {
		return errors.New("expected rejection")
	}
	if t, err := check.Infer(check.V("y"), env); err != nil || !t.Equal(types.Int) {
		return errors.New("env mutated after rejection")
	}
	if _, err := check.Infer(check.V("z"), env); !errors.Is(err, check.ErrUndefined) {
		return errors.New("rejection leaked binding into env")
	}
	return nil
}

package constexpr

import (
	"math/big"
	"testing"
)

func bi(s string) *big.Int {
	x, ok := new(big.Int).SetString(s, 0)
	if !ok {
		panic("bad int " + s)
	}
	return x
}

func br(s string) *big.Rat {
	x, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("bad rat " + s)
	}
	return x
}

func mustEval(t *testing.T, e *Expr) *Value {
	t.Helper()
	v, err := Eval(e)
	if err != nil {
		t.Fatalf("求值意外失败: %v", err)
	}
	return v
}

func mustEvalOn(t *testing.T, ev *Evaluator, e *Expr) *Value {
	t.Helper()
	v, err := ev.Eval(e)
	if err != nil {
		t.Fatalf("求值意外失败: %v", err)
	}
	return v
}

func wantCode(t *testing.T, e *Expr, code EvalCode) {
	t.Helper()
	v, err := Eval(e)
	if err == nil {
		t.Fatalf("期望错误 %s，但求值成功: kind=%s", code, v.Kind())
	}
	ee, ok := err.(*EvalError)
	if !ok || ee.Code != code {
		t.Fatalf("期望错误 %s，实际: %v", code, err)
	}
}

func wantCodeReg(t *testing.T, r *Registry, name string, e *Expr, code EvalCode) {
	t.Helper()
	err := r.Register(name, e)
	if err == nil {
		t.Fatalf("期望登记错误 %s，但成功", code)
	}
	if ee, ok := err.(*EvalError); !ok || ee.Code != code {
		t.Fatalf("期望错误 %s，实际: %v", code, err)
	}
}

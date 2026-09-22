package safeexpr

import (
	"errors"
	"math"
	"testing"
)

func TestEvalValues(t *testing.T) {
	cases := []struct {
		expr string
		want any
	}{
		{"1+2", int64(3)},
		{"1-2-3", int64(-4)},
		{"2*3+4", int64(10)},
		{"2+3*4", int64(14)},
		{"8/2/2", int64(2)},
		{"7/2*2", int64(7)},
		{"10/4", float64(2.5)},
		{"-2*3", int64(-6)},
		{"-2*-3", int64(6)},
		{"1/-2", float64(-0.5)},
		{"1+-2", int64(-1)},
		{"--3", int64(3)},
		{"-(-2)", int64(2)},
		{"0.5+0.25", float64(0.75)},
		{"  ( 1 + 2 ) * 3  ", int64(9)},
		{"1.5*2", int64(3)},
		{"2*-0.5", int64(-1)},
	}
	for _, c := range cases {
		got, err := Eval(c.expr)
		if err != nil {
			t.Fatalf("Eval(%q) 错误: %v", c.expr, err)
		}
		if !equalResult(got, c.want) {
			t.Errorf("Eval(%q) = %v(%T), 期望 %v(%T)", c.expr, got, got, c.want, c.want)
		}
	}
}

func equalResult(a, b any) bool {
	switch av := a.(type) {
	case int64:
		bv, ok := b.(int64)
		return ok && av == bv
	case float64:
		bv, ok := b.(float64)
		return ok && av == bv
	default:
		return false
	}
}

func TestReturnTypeBoundary(t *testing.T) {
	// 可整除除法返回 int64，且类型不是 float64。
	v, err := Eval("6/3")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := v.(int64); !ok {
		t.Fatalf("6/3 应为 int64，实际 %T", v)
	}

	// 非整除除法按有理数转换后返回 float64。
	v, err = Eval("5/2")
	if err != nil {
		t.Fatal(err)
	}
	if f, ok := v.(float64); !ok || f != 2.5 {
		t.Fatalf("5/2 应为 2.5(float64)，实际 %v(%T)", v, v)
	}

	// 中间结果可以巨大，但最终整数必须落在 int64 内。
	v, err = Eval("(9223372036854775807 * 2) / 2")
	if err != nil {
		t.Fatal(err)
	}
	if v != int64(math.MaxInt64) {
		t.Fatalf("大中间值结果 = %v, 期望 %d", v, int64(math.MaxInt64))
	}
}

func TestOverflow(t *testing.T) {
	exprs := []string{
		"9223372036854775807+1",
		"-9223372036854775808-1",
		"9223372036854775807*2",
		"9223372036854775809",
	}
	for _, expr := range exprs {
		_, err := Eval(expr)
		if !errors.Is(err, ErrOverflow) {
			t.Errorf("Eval(%q) 错误 = %v, 期望 ErrOverflow", expr, err)
		}
	}

	// int64 边界值应成功，绝不绕回。
	if v, err := Eval("9223372036854775807"); err != nil || v != int64(math.MaxInt64) {
		t.Errorf("MaxInt64 字面值: v=%v err=%v", v, err)
	}
	if v, err := Eval("-9223372036854775808"); err != nil || v != int64(math.MinInt64) {
		t.Errorf("MinInt64 字面值: v=%v err=%v", v, err)
	}
}

func TestDivisionByZero(t *testing.T) {
	exprs := []string{"1/0", "2/(3-3)", "-5/0", "0/0"}
	for _, expr := range exprs {
		_, err := Eval(expr)
		if !errors.Is(err, ErrDivisionByZero) {
			t.Errorf("Eval(%q) 错误 = %v, 期望 ErrDivisionByZero", expr, err)
		}
		if pe, ok := err.(*Error); ok && pe.Pos < 0 {
			t.Errorf("Eval(%q) 除零错误缺少位置", expr)
		}
	}
}

func TestInexactLiteral(t *testing.T) {
	exprs := []string{"0.1", "0.3", "-0.1", "10*0.1", "0.1+0.2"}
	for _, expr := range exprs {
		_, err := Eval(expr)
		if !errors.Is(err, ErrInexact) {
			t.Errorf("Eval(%q) 错误 = %v, 期望 ErrInexact", expr, err)
		}
	}

	// 可精确表示的小数不得误报。
	for _, expr := range []string{"0.5", "0.25", "0.125", "0.5+0.5"} {
		if _, err := Eval(expr); err != nil {
			t.Errorf("Eval(%q) 不应报错: %v", expr, err)
		}
	}

	// 1/3 不是十进制字面量，按有理数转 float64 返回近似值而不是错误。
	v, err := Eval("1/3")
	if err != nil {
		t.Fatalf("1/3 不应报错: %v", err)
	}
	if _, ok := v.(float64); !ok {
		t.Fatalf("1/3 应为 float64，实际 %T", v)
	}
}

func TestInputNotMutated(t *testing.T) {
	expr := "  1 + 2 * 3  "
	original := expr
	if _, err := Eval(expr); err != nil {
		t.Fatal(err)
	}
	if expr != original {
		t.Fatalf("输入字符串被修改: %q -> %q", original, expr)
	}
}

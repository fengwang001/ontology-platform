package ontology

import (
	"errors"
	"testing"
)

// TestEvalTypes 断言返回类型边界：整数结果给 int64，非整数给 float64。
func TestEvalTypes(t *testing.T) {
	cases := []struct {
		in   string
		want any
	}{
		{"2+3", int64(5)},
		{"4/2", int64(2)},       // 整除给 int64
		{"1/2", float64(0.5)},   // 不整除给 float64
		{"2/4", float64(0.5)},   // 约分后仍非整数
		{"1/8", float64(0.125)}, // 二进制可精确表示
		{"0.5+0.5", int64(1)},   // 小数运算结果可以是整数
		{"0.25", float64(0.25)},
		{"1.5*2", int64(3)},
		{"0.1*10", int64(1)}, // 中间过程精确，结果为整数
		{"7-10", int64(-3)},
		{"-6/3", int64(-2)},
		{".5", float64(0.5)},
		{"5.", int64(5)},
		{"9223372036854775807", int64(9223372036854775807)},
		{"-9223372036854775808", int64(-9223372036854775808)},
	}
	for _, c := range cases {
		got, err := Eval(c.in)
		if err != nil {
			t.Errorf("Eval(%q) 出错: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("Eval(%q) = %#v (%T), 期望 %#v (%T)",
				c.in, got, got, c.want, c.want)
		}
	}
}

// TestEvalInexact 十进制或分数结果无法被 float64 精确表示时必须报错。
func TestEvalInexact(t *testing.T) {
	for _, in := range []string{"0.1", "0.1+0.2", "1/3", "0.2/0.3", "0.3-0.1"} {
		_, err := Eval(in)
		if !errors.Is(err, ErrInexact) {
			t.Errorf("Eval(%q) err = %v, 期望 ErrInexact", in, err)
		}
	}
}

// TestEvalOverflow int64 溢出必须报错，不得绕回。
func TestEvalOverflow(t *testing.T) {
	for _, in := range []string{
		"9223372036854775807+1",
		"9223372036854775808",
		"9223372036854775807*2",
		"-9223372036854775808-1",
		"9223372036854775807+9223372036854775807",
	} {
		_, err := Eval(in)
		if !errors.Is(err, ErrOverflow) {
			t.Errorf("Eval(%q) err = %v, 期望 ErrOverflow", in, err)
		}
	}
}

// TestEvalDivZero 除零必须报错。
func TestEvalDivZero(t *testing.T) {
	for _, in := range []string{"1/0", "0/0", "1/(1-1)", "1/(2-2*1)"} {
		_, err := Eval(in)
		if !errors.Is(err, ErrDivZero) {
			t.Errorf("Eval(%q) err = %v, 期望 ErrDivZero", in, err)
		}
	}
}

// TestEvalWhitespace 任意层空白不影响求值。
func TestEvalWhitespace(t *testing.T) {
	got, err := Eval(" \t\n ( 1 + 2 ) * 3\n- 8 / 4  ")
	if err != nil {
		t.Fatalf("Eval 出错: %v", err)
	}
	if got != int64(7) {
		t.Fatalf("Eval = %#v, 期望 int64(7)", got)
	}
}

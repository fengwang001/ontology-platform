package conv

import (
	"errors"
	"strings"
	"testing"

	"ontology/lex"
)

// TestConvertKnownValues 钉住八行表与典型有限/循环小数的精确既约结果。
func TestConvertKnownValues(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"3.14", "157/50"}, {"0.1", "1/10"}, {"-2.5", "-5/2"},
		{"0.(3)", "1/3"}, {"0.1(6)", "1/6"}, {"123", "123/1"},
		{"0.(142857)", "1/7"}, {"-0.0", "0/1"},
		{"1.(285714)", "9/7"}, {"0.(0)", "0/1"}, {"1.0", "1/1"},
		{"-0.(9)", "-1/1"}, {"0.111", "111/1000"},
	}
	for _, c := range cases {
		p, err := lex.Scan(c.in)
		if err != nil {
			t.Fatalf("%q scan: %v", c.in, err)
		}
		f, err := Convert(p)
		if err != nil {
			t.Fatalf("%q convert: %v", c.in, err)
		}
		if f.String() != c.want {
			t.Errorf("%q = %s, want %s", c.in, f, c.want)
		}
	}
}

// TestConvertOverflow 钉住溢出可判定：19 个 9 超 MaxInt64，
// 朴素 int64 会静默回绕，正确实现必须返回 ErrOverflow。
func TestConvertOverflow(t *testing.T) {
	cases := []string{
		"0." + strings.Repeat("9", 19),
		strings.Repeat("9", 19),
		"0.(" + strings.Repeat("9", 19) + ")",
	}
	for _, in := range cases {
		p, _ := lex.Scan(in)
		if _, err := Convert(p); !errors.Is(err, ErrOverflow) {
			t.Errorf("%q: got %v, want ErrOverflow", in, err)
		}
	}
	// 边界内：18 个 9 必须成功。
	p, _ := lex.Scan(strings.Repeat("9", 18))
	if _, err := Convert(p); err != nil {
		t.Errorf("18 nines should fit, got %v", err)
	}
}

// TestMul10SinglePass 证明单趟线性 O(m)：对 100..10000 位的有限小数，
// 「乘以 10」计数必须恰好等于位数 m（溢出也照常扫完、计数完整）。
// 计数器是非导出字段，本测试在 conv 包内直接读 converter，
// 不经由任何导出函数或方法接触它。
func TestMul10SinglePass(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		in := "0." + strings.Repeat("1", m)
		p, err := lex.Scan(in)
		if err != nil {
			t.Fatalf("m=%d scan: %v", m, err)
		}
		c := &converter{}
		_, _ = c.convert(p) // m≥19 时数值溢出，但计数仍须完整
		if c.mul10 != m {
			t.Errorf("m=%d mul10=%d, want exactly %d (single pass)", m, c.mul10, m)
		}
	}
	// 无溢出小样本：计数与结果同时核验。
	p, _ := lex.Scan("0.111")
	c := &converter{}
	f, err := c.convert(p)
	if err != nil || c.mul10 != 3 || f.String() != "111/1000" {
		t.Errorf("0.111: f=%v mul10=%d err=%v", f, c.mul10, err)
	}
}

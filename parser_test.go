package ontology

import "testing"

// TestParseStructure 同时断言解析结构与求值结果，保证同一表达式
// 只有唯一解析树：优先级 括号 > 一元负号 > * / > + -，同级左结合。
func TestParseStructure(t *testing.T) {
	cases := []struct {
		in    string
		want  string
		value any
	}{
		{"1-2-3", "((1-2)-3)", int64(-4)},
		{"1+2+3", "((1+2)+3)", int64(6)},
		{"8/4/2", "((8/4)/2)", int64(1)},
		{"1+2*3", "(1+(2*3))", int64(7)},
		{"2*3+4", "((2*3)+4)", int64(10)},
		{"2*(3+4)", "(2*(3+4))", int64(14)},
		{"(1+2)*3", "((1+2)*3)", int64(9)},
		{"-2*3", "(-2*3)", int64(-6)},
		{"--3", "-(-3)", int64(3)},
		{"-(-2)", "-(-2)", int64(2)},
		{"-2*-3", "(-2*-3)", int64(6)},
		{"1/-2", "(1/-2)", float64(-0.5)},
		{"1+-2", "(1+-2)", int64(-1)},
		{"-(1+2)", "-((1+2))", int64(-3)},
		{" 1 - 2 - 3 ", "((1-2)-3)", int64(-4)},
		{"-  -  3", "-(-3)", int64(3)},
	}
	for _, c := range cases {
		n, err := Parse(c.in)
		if err != nil {
			t.Errorf("Parse(%q) 出错: %v", c.in, err)
			continue
		}
		if got := n.String(); got != c.want {
			t.Errorf("Parse(%q) 结构 = %q, 期望 %q", c.in, got, c.want)
		}
		v, err := Eval(c.in)
		if err != nil {
			t.Errorf("Eval(%q) 出错: %v", c.in, err)
			continue
		}
		if v != c.value {
			t.Errorf("Eval(%q) = %#v, 期望 %#v", c.in, v, c.value)
		}
	}
}

// TestParseUniqueness 写法不同（空白、冗余括号）但语义相同的表达式
// 必须得到完全相同的解析结构。
func TestParseUniqueness(t *testing.T) {
	variants := []string{
		"1-2-3",
		"1 - 2 - 3",
		"  1- 2 -3  ",
		"((1-2)-3)",
		"(1-2)-3",
		"((1) - (2)) - (3)",
	}
	const want = "((1-2)-3)"
	for _, s := range variants {
		n, err := Parse(s)
		if err != nil {
			t.Fatalf("Parse(%q) 出错: %v", s, err)
		}
		if got := n.String(); got != want {
			t.Errorf("Parse(%q) 结构 = %q, 期望 %q", s, got, want)
		}
	}
}

// TestImplicitMultiplicationRejected 数字直接跟括号是非法的。
func TestImplicitMultiplicationRejected(t *testing.T) {
	for _, s := range []string{"2(3)", "(1)(2)", "2 3"} {
		if _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q) 应当报错", s)
		}
	}
}

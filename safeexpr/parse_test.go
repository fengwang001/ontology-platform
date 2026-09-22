package safeexpr

import (
	"errors"
	"testing"
)

func TestParseStructure(t *testing.T) {
	cases := []struct {
		expr string
		want string
	}{
		{"1-2-3", "(- (- 1 2) 3)"},
		{"1-2+3", "(+ (- 1 2) 3)"},
		{"8/2/2", "(/ (/ 8 2) 2)"},
		{"2+3*4", "(+ 2 (* 3 4))"},
		{"2*3+4", "(+ (* 2 3) 4)"},
		{"2*(3+4)", "(* 2 (+ 3 4))"},
		{"-2*3", "(* (- 2) 3)"},
		{"-2*-3", "(* (- 2) (- 3))"},
		{"1/-2", "(/ 1 (- 2))"},
		{"1+-2", "(+ 1 (- 2))"},
		{"--3", "(- (- 3))"},
		{"-(-2)", "(- (- 2))"},
		{"-(2*3)", "(- (* 2 3))"},
		{"  ( 1 + 2 ) * 3 ", "(* (+ 1 2) 3)"},
		{"1.5 + 2.25", "(+ 1.5 2.25)"},
	}
	for _, c := range cases {
		node, err := Parse(c.expr)
		if err != nil {
			t.Fatalf("Parse(%q) 返回错误: %v", c.expr, err)
		}
		if got := node.String(); got != c.want {
			t.Errorf("Parse(%q) 结构 = %q, 期望 %q", c.expr, got, c.want)
		}
	}
}

func TestParseDeterministic(t *testing.T) {
	// 同一字符串多次扫描，解析树必须完全一致。
	exprs := []string{
		"1-2-3",
		"-2*-3",
		"((1+2)*(3-4))/-2",
		"---5 + 2 * 3 - 1 / 4",
	}
	for _, expr := range exprs {
		first, err := Parse(expr)
		if err != nil {
			t.Fatalf("Parse(%q) 错误: %v", expr, err)
		}
		for i := 0; i < 20; i++ {
			got, err := Parse(expr)
			if err != nil {
				t.Fatalf("第 %d 次 Parse(%q) 错误: %v", i, expr, err)
			}
			if got.String() != first.String() {
				t.Fatalf("Parse(%q) 不唯一: %q vs %q", expr, got.String(), first.String())
			}
		}
	}
}

func TestParseReject(t *testing.T) {
	cases := []struct {
		expr string
		kind error
		pos  int
	}{
		{"2(3)", ErrIllegalChar, 1},
		{"(1)2", ErrIllegalChar, 3},
		{"(1)(2)", ErrIllegalChar, 3},
		{"(1+2", ErrUnmatchedParen, 0},
		{"1+2)", ErrUnmatchedParen, 3},
		{"1.2.3", ErrMalformedNumber, 3},
		{"..5", ErrMalformedNumber, 0},
		{"5.", ErrMalformedNumber, 1},
		{"1+*2", ErrSyntax, 2},
		{"*3", ErrSyntax, 0},
	}
	for _, c := range cases {
		_, err := Parse(c.expr)
		if err == nil {
			t.Errorf("Parse(%q) 应失败却成功", c.expr)
			continue
		}
		if !errors.Is(err, c.kind) {
			t.Errorf("Parse(%q) 错误类别 = %v, 期望 %v", c.expr, err, c.kind)
			continue
		}
		pe, ok := err.(*Error)
		if !ok {
			t.Fatalf("Parse(%q) 错误不是 *Error", c.expr)
		}
		if pe.Pos != c.pos {
			t.Errorf("Parse(%q) 位置 = %d, 期望 %d", c.expr, pe.Pos, c.pos)
		}
	}
}

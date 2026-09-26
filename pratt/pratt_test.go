package pratt

import (
	"errors"
	"strings"
	"testing"

	"ontology/lex"
)

func evalStr(t *testing.T, s string) (int64, error) {
	t.Helper()
	toks, err := lex.Tokenize(s)
	if err != nil {
		t.Fatal(err)
	}
	n, err := Parse(toks)
	if err != nil {
		return 0, err
	}
	return Eval(n)
}

func TestEvalTable(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"10-4-3", 3}, {"2^3^2", 512}, {"-2^2", 4}, {"2^10", 1024},
		{"- -3", 3}, {"--3", 3}, {"1- -2", 3}, {"(1+2)^2", 9},
		{"7/2", 3}, {"0-7/2", -3}, {"7/(0-2)", -3}, {"2^0", 1},
		{"0^0", 1}, {"0^5", 0}, {"3-2-1", 0}, {"100/10/5", 2},
		{"2+3*4", 14}, {"2*3+4", 10}, {"1+2*3^2", 19}, {"((5))", 5},
	}
	for _, c := range cases {
		if got, err := evalStr(t, c.in); err != nil || got != c.want {
			t.Errorf("%s = %d, %v; want %d", c.in, got, err, c.want)
		}
	}
}

// TestRightAssoc：^ 右结合（不变量 2）。
func TestRightAssoc(t *testing.T) {
	for in, want := range map[string]int64{"2^3^2": 512, "2^2^3": 256, "3^2^2": 81, "2^1^1": 2} {
		if got, err := evalStr(t, in); err != nil || got != want {
			t.Errorf("%s = %d, %v; want %d（右结合）", in, got, err, want)
		}
	}
}

// TestPrefixInfix：前缀一元 - 与中缀 - 同次解析正确区分（不变量 3）。
func TestPrefixInfix(t *testing.T) {
	for in, want := range map[string]int64{"- -3": 3, "5- -3": 8, "-2^2": 4, "1- -2": 3, "0--2^2": -4} {
		if got, err := evalStr(t, in); err != nil || got != want {
			t.Errorf("%s = %d, %v; want %d", in, got, err, want)
		}
	}
}

func TestErrorTable(t *testing.T) {
	cases := []struct {
		in   string
		want error
	}{
		{"", ErrSyntax}, {"1+", ErrSyntax}, {"(1+2", ErrParen}, {"1+2)", ErrParen},
		{"()", ErrSyntax}, {"1 2", ErrSyntax}, {"1/0", ErrDivZero}, {"1/(3-3)", ErrDivZero},
		{"2^-1", ErrBadExp}, {"10^19", ErrOverflow}, {"2^63", ErrOverflow},
	}
	for _, c := range cases {
		if _, err := evalStr(t, c.in); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v; want %v", c.in, err, c.want)
		}
	}
}

// TestCompareEarlyStop：以 min_bp=40 的层解析 2^2^...^2（即前缀一元 - 的操作数层），
// 第一个 ^（lbp=30<40）即停止，比较的运算符个数不随 m 增长（m=100..10000 恒为 1）。
func TestCompareEarlyStop(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		toks, err := lex.Tokenize(strings.Repeat("2^", m) + "2")
		if err != nil {
			t.Fatal(err)
		}
		p := &parser{toks: toks}
		n := p.parseExpr(unaryBP)
		if n.Op != lex.Num || n.Val != 2 {
			t.Fatalf("m=%d: 应在第一个数字后立即停止", m)
		}
		if p.cmps != 1 {
			t.Fatalf("m=%d: 比较了 %d 个运算符，应恒为 1（与 m 无关）", m, p.cmps)
		}
	}
}

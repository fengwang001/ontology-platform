package parse

import (
	"errors"
	"ontology/lex"
	"strings"
	"testing"
)

func tokenize(t *testing.T, s string) []lex.Token {
	t.Helper()
	toks, err := lex.Tokenize(s)
	if err != nil {
		t.Fatalf("tokenize %q: %v", s, err)
	}
	return toks
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		in   string
		want error
	}{
		{"", ErrEmpty},
		{"(", ErrParen},
		{")", ErrParen},
		{"(1", ErrParen},
		{"1)", ErrParen},
		{"((1+2)", ErrParen},
		{"1 2", ErrTrailing},
		{"1+", ErrSyntax},
		{"*1", ErrSyntax},
		{"1**2", ErrSyntax},
	}
	for _, c := range cases {
		_, err := Parse(tokenize(t, c.in))
		if !errors.Is(err, c.want) {
			t.Errorf("Parse(%q) err = %v, want %v", c.in, err, c.want)
		}
	}
}

func TestASTLeftAssocShape(t *testing.T) {
	cases := []struct {
		in              string
		rootOp          string
		leftOp, rightOp string
	}{
		{"8-3-2", OpSub, OpSub, OpNum},
		{"8/2/2", OpDiv, OpDiv, OpNum},
		{"2+3*4", OpAdd, OpNum, OpMul}, // * 优先级高于 +
		{"-7/2", OpDiv, OpNeg, OpNum},  // 一元 - 高于 /
	}
	for _, c := range cases {
		n, err := Parse(tokenize(t, c.in))
		if err != nil {
			t.Fatalf("Parse(%q): %v", c.in, err)
		}
		if n.Op != c.rootOp || n.Left.Op != c.leftOp || n.Right.Op != c.rightOp {
			t.Errorf("Parse(%q) shape root=%s left=%s right=%s",
				c.in, n.Op, n.Left.Op, n.Right.Op)
		}
	}
}

// TestLookaheadPeak 是唯一允许直接读非导出计数器 peak 的地方：
// 多档 m（100..10000）下前瞻缓冲峰值必须恒为 1，与 m 无关。
func TestLookaheadPeak(t *testing.T) {
	ms := []int{100, 500, 1000, 5000, 10000}
	for _, m := range ms {
		s := strings.Repeat("1+", m-1) + "1" // m 个叶子
		p := &parser{src: tokenize(t, s)}
		if _, err := p.expr(); err != nil {
			t.Fatalf("m=%d parse: %v", m, err)
		}
		if p.peak > 1 {
			t.Fatalf("m=%d lookahead peak = %d, want <= 1", m, p.peak)
		}
	}
	// 深嵌套括号同样保持单记号前瞻。
	p := &parser{src: tokenize(t, strings.Repeat("(", 2000)+"1"+strings.Repeat(")", 2000))}
	if _, err := p.expr(); err != nil {
		t.Fatal(err)
	}
	if p.peak > 1 {
		t.Fatalf("nested paren lookahead peak = %d, want <= 1", p.peak)
	}
}

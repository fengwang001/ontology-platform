package parse

import (
	"errors"
	"strings"
	"testing"

	"ontology/lex"
)

// TestLookaheadPeak 证明解析全程前瞻缓冲峰值 <= 1（与 m 无关的常数），
// 即 LL(1) 单记号前瞻，而非把整条记号流预读进缓冲。
// 本测试与计数器同包，直接读非导出变量 peak，不经过任何导出接口。
func TestLookaheadPeak(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		src := "1" + strings.Repeat("+1", m-1) // m 个叶子
		toks, err := lex.Lex(src)
		if err != nil {
			t.Fatalf("m=%d lex: %v", m, err)
		}
		peak.Store(0)
		root, err := Parse(toks)
		if err != nil {
			t.Fatalf("m=%d parse: %v", m, err)
		}
		if root == nil {
			t.Fatalf("m=%d: nil root", m)
		}
		if got := peak.Load(); got > 1 {
			t.Fatalf("m=%d: lookahead peak = %d, want <= 1", m, got)
		}
	}
}

// TestParseErrors 表驱动钉住三类可判定解析错误互不相同。
func TestParseErrors(t *testing.T) {
	cases := []struct {
		src  string
		want error
	}{
		{"", ErrOperand},
		{"1+", ErrOperand},
		{"+1", ErrOperand},
		{"(1", ErrParen},
		{"1)", ErrParen},
		{"()", ErrParen},
		{"1 2", ErrTrailing},
	}
	for _, c := range cases {
		toks, _ := lex.Lex(c.src)
		_, err := Parse(toks)
		if !errors.Is(err, c.want) {
			t.Fatalf("%q: got %v, want %v", c.src, err, c.want)
		}
		for _, other := range []error{ErrOperand, ErrTrailing, ErrParen} {
			if other != c.want && errors.Is(err, other) {
				t.Fatalf("%q: %v must differ from %v", c.src, err, other)
			}
		}
	}
}

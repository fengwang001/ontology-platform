package api

import (
	"errors"
	"testing"
)

func TestCompileMatchAPI(t *testing.T) {
	// 对外 Compile/Match 的接受结果（含第三节甲乙丙三问的正确解析）。
	cases := []struct {
		p, s string
		w    bool
	}{
		{"a(b|c)*", "a", true}, {"a(b|c)*", "ab", true}, {"a(b|c)*", "abb", true},
		{"a(b|c)*", "b", false}, {"a(b|c)*", "", false},
		{"ab|cd", "ab", true}, {"ab|cd", "cd", true}, {"ab|cd", "bd", false},
		{"ab*", "a", true}, {"ab*", "abb", true}, {"ab*", "", false}, {"ab*", "b", false},
		{"(a|b)*", "", true}, {"(a|b)*", "abba", true}, {"(a|b)*", "c", false},
		{"a+", "", false}, {"a+", "aaa", true}, {"a?", "a", true}, {"a?", "", true},
	}
	for _, c := range cases {
		q, err := Compile(c.p)
		if err != nil {
			t.Fatalf("Compile(%q): %v", c.p, err)
		}
		if g := q.Match(c.s); g != c.w {
			t.Fatalf("Compile(%q).Match(%q)=%v want %v", c.p, c.s, g, c.w)
		}
		if g, err := Match(c.p, c.s); err != nil || g != c.w {
			t.Fatalf("Match(%q,%q)=(%v,%v) want %v", c.p, c.s, g, err, c.w)
		}
	}
}

// 不变量 4：四类非法 pattern 各返回专属哨兵，互不相同，且不产生可用 NFA。
func TestRejectErrorsDistinct(t *testing.T) {
	cases := []struct {
		p   string
		err error
	}{
		{"", ErrEmptyPattern},
		{"a1", ErrIllegalChar}, {"A", ErrIllegalChar}, {"a b", ErrIllegalChar}, {".", ErrIllegalChar},
		{"(a", ErrUnbalancedParen}, {"a)", ErrUnbalancedParen}, {"((a)", ErrUnbalancedParen},
		{"*", ErrMissingOperand}, {"a|", ErrMissingOperand}, {"()", ErrMissingOperand}, {"(|a)*", ErrMissingOperand},
	}
	for _, c := range cases {
		q, err := Compile(c.p)
		if q != nil || !errors.Is(err, c.err) {
			t.Fatalf("Compile(%q)=(%v,%v) want err %v", c.p, q, err, c.err)
		}
	}
	sentinels := []error{ErrEmptyPattern, ErrIllegalChar, ErrUnbalancedParen, ErrMissingOperand}
	for i := range sentinels {
		for j := i + 1; j < len(sentinels); j++ {
			if errors.Is(sentinels[i], sentinels[j]) {
				t.Fatalf("sentinels %d and %d must be distinct", i, j)
			}
		}
	}
}

// 不变量 4：反复失败不影响后续调用（无全局状态残留）。
func TestRejectionLeavesNoTrace(t *testing.T) {
	bad := []string{"", "a1", "(a", "*", "()", "a)", "a|b|"}
	for round := 0; round < 3; round++ {
		for _, p := range bad {
			if q, err := Compile(p); q != nil || err == nil {
				t.Fatalf("round %d: Compile(%q) should fail", round, p)
			}
		}
		q, err := Compile("ab|cd")
		if err != nil || !q.Match("ab") || q.Match("bd") {
			t.Fatalf("round %d: state not clean after rejections", round)
		}
		if ok, err := Match("(a|b)*", "abba"); err != nil || !ok {
			t.Fatalf("round %d: later Match broken", round)
		}
	}
}

func TestSelfCheck(t *testing.T) {
	if err := SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

package match

import (
	"math/rand"
	"strings"
	"testing"

	"ontology/parse"
)

func mustToks(t *testing.T, p string) []parse.Token {
	t.Helper()
	toks, err := parse.Parse(p)
	if err != nil {
		t.Fatalf("Parse(%q) = %v", p, err)
	}
	return toks
}

// 不变量 3：`.` 恰好匹配一个字符，`*` 取零次或多次。
func TestDotStarSemantics(t *testing.T) {
	cases := []struct {
		s, p string
		want bool
	}{
		{"a", ".", true}, {"", ".", false}, {"ab", ".", false},
		{"", "a*", true}, {"aaa", "a*", true}, {"aaab", "a*", false},
		{"", ".*", true}, {"xyz", ".*", true},
		{"", "a*b*", true}, {"aab", "a*b*", true}, {"aabb", "a*b*", true}, {"aba", "a*b*", false},
		{"ab", "a.", true}, {"a", "a.", false},
		{"aab", "c*a*b", true}, {"aab", "c*a*b*", true},
	}
	for _, c := range cases {
		if got := Match(c.s, c.p); got != c.want {
			t.Errorf("Match(%q,%q) = %v, want %v", c.s, c.p, got, c.want)
		}
	}
}

// 不变量 1：记忆化 DP 与朴素回溯逐对相同（表驱动 + 随机循环）。
func TestMatchConsistentWithNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(762))
	for round := 0; round < 300; round++ {
		var pb strings.Builder
		for k := 0; k < 1+rng.Intn(5); k++ {
			if rng.Intn(3) == 0 {
				pb.WriteByte('.')
			} else {
				pb.WriteByte(byte('a' + rng.Intn(3)))
			}
			if rng.Intn(2) == 0 {
				pb.WriteByte('*')
			}
		}
		var sb strings.Builder
		for i := 0; i < rng.Intn(9); i++ {
			sb.WriteByte(byte('a' + rng.Intn(3)))
		}
		p, s := pb.String(), sb.String()
		if Match(s, p) != Naive(s, mustToks(t, p)) {
			t.Fatalf("Match(%q,%q) 与朴素回溯不一致", s, p)
		}
	}
}

// 不变量 2：同一实现开/关记忆化，结果逐对相同。
func TestMemoSameAsNoMemo(t *testing.T) {
	cases := []struct{ s, p string }{
		{"aab", "c*a*b"}, {"mississippi", "mis*is*p*."}, {"", "a*b*"},
		{"aaaab", "a*a*a*b"}, {"xyz", ".*"}, {"ab", "a.*b"},
	}
	for _, c := range cases {
		toks := mustToks(t, c.p)
		if matchTokens(c.s, toks, true) != matchTokens(c.s, toks, false) {
			t.Errorf("(%q,%q) 记忆化前后结果不同", c.s, c.p)
		}
	}
}

// 复杂度：固定 n=6 的模式，求值状态数不超过 (n+1)·(m+1)，不随 m 指数增长。
func TestStateCountLinearInM(t *testing.T) {
	const p = "a*a*a*" // n = 6；朴素回溯在同输入下是指数级
	toks := mustToks(t, p)
	n := len(p)
	for _, m := range []int{100, 1000, 5000, 10000} {
		evaluated.Store(0)
		if !MatchTokens(strings.Repeat("a", m), toks) {
			t.Fatalf("m=%d 应匹配", m)
		}
		got, limit := evaluated.Load(), int64(n+1)*int64(m+1)
		if got > limit {
			t.Fatalf("m=%d 求值状态数 %d 超过 (n+1)(m+1)=%d", m, got, limit)
		}
	}
}

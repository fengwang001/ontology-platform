package nfa

import (
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"

	"ontology/reast"
)

func mustParse(t *testing.T, p string) *reast.RE {
	t.Helper()
	r, err := reast.Parse(p)
	if err != nil {
		t.Fatalf("parse %q: %v", p, err)
	}
	return r
}

// stringsN 循环生成 {a,b,c} 上长度 0..n 的全部串（含空串）。
func stringsN(n int) []string {
	var out []string
	var gen func(string)
	gen = func(p string) {
		out = append(out, p)
		if len(p) < n {
			for _, c := range "abc" {
				gen(p + string(c))
			}
		}
	}
	gen("")
	return out
}

func TestNFAEqualsReference(t *testing.T) {
	// 不变量 1：NFA 逐串等于 AST 朴素回溯参照（表驱动模式 × 循环枚举输入）。
	pats := []string{"a", "ab", "a|b", "a*", "a+", "a?", "a(b|c)*", "(ab)+", "a?b+", "(a|b)*c", "(ab|a)(c|d)*", "x+y*z?", "((a))", "(a|b|c)+"}
	for _, p := range pats {
		r := mustParse(t, p)
		for _, s := range stringsN(5) {
			if g, w := Compile(r).Match(s), reast.ReferenceMatch(r, s); g != w {
				t.Fatalf("%q Match(%q)=%v ref=%v", p, s, g, w)
			}
		}
	}
}

func TestClosureIdempotent(t *testing.T) {
	// 不变量 2：逐字符推进时，每个字符步之后的状态集都等于自身 ε 传递闭包。
	for _, p := range []string{"a(b|c)*", "(a|b)*c", "ab|cd", "a+b?c*"} {
		q := Compile(mustParse(t, p))
		for _, s := range stringsN(5) {
			cur := closure(q, map[int]struct{}{q.Start: {}})
			if !maps.Equal(cur, closure(q, cur)) {
				t.Fatalf("%q initial not ε-closed", p)
			}
			for k := 0; k < len(s); k++ {
				nxt := map[int]struct{}{}
				for st := range cur {
					for _, e := range q.Char[st] {
						if e.Ch == s[k] {
							nxt[e.To] = struct{}{}
						}
					}
				}
				cur = closure(q, nxt)
				if !maps.Equal(cur, closure(q, cur)) {
					t.Fatalf("%q after %q not ε-closed", p, s[:k+1])
				}
			}
		}
	}
}

func TestSuffixExpansion(t *testing.T) {
	// 不变量 3：R+≡R·R*、R?≡R|ε、R* 接受空串，与展开式逐串同语言。
	ss := stringsN(5)
	eq := func(q1, q2 *NFA) {
		for _, s := range ss {
			if q1.Match(s) != q2.Match(s) {
				t.Fatalf("expansion mismatch on %q", s)
			}
		}
	}
	for _, pr := range [][2]string{{"a+", "aa*"}, {"(ab)+", "ab(ab)*"}, {"(a|c)+", "(a|c)(a|c)*"}} {
		eq(Compile(mustParse(t, pr[0])), Compile(mustParse(t, pr[1])))
	}
	for _, p := range []string{"a", "ab", "a|b", "x+y"} {
		x := mustParse(t, p).Root
		eps := &reast.Node{Kind: reast.Eps}
		exp := Compile(&reast.RE{Root: &reast.Node{Kind: reast.Alt, L: x, R: eps}})
		eq(Compile(mustParse(t, "("+p+")?")), exp)
	}
	for _, p := range []string{"a*", "(a|b)*", "(ab)*"} {
		if !Compile(mustParse(t, p)).Match("") {
			t.Fatalf("%q must accept empty", p)
		}
	}
}

func TestAppendTouchesConstantStates(t *testing.T) {
	// 复杂度：m 档循环，追加一个字符触碰既有状态恒为与 m 无关的小常数。
	for _, m := range []int{100, 1000, 10000} {
		q := Compile(mustParse(t, strings.Repeat("a", m)))
		before := len(q.Char)
		q.Append('z')
		if q.touched < 1 || q.touched > 2 {
			t.Fatalf("m=%d touched=%d want constant", m, q.touched)
		}
		if len(q.Char) != before+2 {
			t.Fatalf("m=%d states +%d want 2", m, len(q.Char)-before)
		}
		if !q.Match(strings.Repeat("a", m)+"z") || q.Match(strings.Repeat("a", m)) {
			t.Fatalf("m=%d appended language wrong", m)
		}
	}
}

func TestConcurrentMatch(t *testing.T) {
	// 并发：N 个 goroutine 并发 Match 同一批串，结果逐值相同（无 sleep）。
	pats, ins := []string{"a(b|c)*", "ab|cd", "ab*", "(a|b)*", "a+", "a?"}, stringsN(4)
	batch := func() []bool {
		r := make([]bool, 0, len(pats)*len(ins))
		for _, p := range pats {
			q := Compile(mustParse(t, p))
			for _, s := range ins {
				r = append(r, q.Match(s))
			}
		}
		return r
	}
	base := batch()
	const N = 16
	var wg sync.WaitGroup
	res := make([][]bool, N)
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) { defer wg.Done(); res[g] = batch() }(g)
	}
	wg.Wait()
	for g := 0; g < N; g++ {
		if !slices.Equal(res[g], base) {
			t.Fatalf("goroutine %d diverged", g)
		}
	}
}

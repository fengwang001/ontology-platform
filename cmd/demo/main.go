package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"ontology/api"
	"ontology/nfa"
	"ontology/reast"
)

func ok(b bool) string {
	if b {
		return "OK"
	}
	return "FAIL"
}

func must(p string) *reast.RE {
	r, err := reast.Parse(p)
	if err != nil {
		panic(err)
	}
	return r
}

func fmtStep(s nfa.Step) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s +%v", s.Op, s.New)
	for _, e := range s.Edges {
		if e.Ch == 0 {
			fmt.Fprintf(&b, " %dε%d", e.From, e.To)
		} else {
			fmt.Fprintf(&b, " %d-%c->%d", e.From, e.Ch, e.To)
		}
	}
	return b.String()
}

func main() {
	// 1) 第三节六步：每步状态与转移必须逐字等于 NOTES 的表
	_, steps := nfa.CompileTrace(must("a(b|c)*"))
	want := []string{
		"char a +[0 1] 0-a->1",
		"char b +[2 3] 2-b->3",
		"char c +[4 5] 4-c->5",
		"alt +[6 7] 6ε2 6ε4 3ε7 5ε7",
		"star +[8 9] 8ε6 8ε9 7ε6 7ε9",
		"concat +[] 1ε8",
	}
	good := len(steps) == 6
	parts := make([]string, 6)
	for i := range want {
		parts[i] = fmtStep(steps[i])
		if parts[i] != want[i] {
			good = false
		}
	}
	fmt.Println("six steps", ok(good)+":", strings.Join(parts, "; "))

	// 2) a(b|c)* 对 5 个串的接受结果
	n := nfa.Compile(must("a(b|c)*"))
	cases := []struct {
		s string
		w bool
	}{{"a", true}, {"ab", true}, {"ac", true}, {"abb", true}, {"b", false}}
	g2 := true
	for _, c := range cases {
		if n.Match(c.s) != c.w {
			g2 = false
		}
	}
	fmt.Println("a(b|c)* a/ab/ac/abb=1 b=0:", ok(g2))

	// 3) 甲乙丙：优先级、后缀绑定、星吃空串
	g3 := nfa.Compile(must("ab|cd")).Match("ab") &&
		nfa.Compile(must("ab*")).Match("a") &&
		nfa.Compile(must("(a|b)*")).Match("")
	fmt.Println("ab|cd⊇ab, ab*⊇a, (a|b)*⊇\"\":", ok(g3))

	// 4) 大 m 下追加一个字符：新增状态恒为 2，语言只在尾部多一个 z
	g4 := true
	var delta int
	for _, m := range []int{100, 1000, 10000} {
		q := nfa.Compile(must(strings.Repeat("a", m)))
		before := len(q.Char)
		q.Append('z')
		delta = len(q.Char) - before
		if delta != 2 || !q.Match(strings.Repeat("a", m)+"z") || q.Match(strings.Repeat("a", m)) {
			g4 = false
		}
	}
	fmt.Println("append O(1) Δstates=2 @m=10000:", ok(g4))

	// 5) 并发：N 个 goroutine 对同一批 pattern×串，结果逐值相同
	pats := []string{"a(b|c)*", "ab|cd", "ab*", "(a|b)*", "a+", "a?"}
	ins := []string{"", "a", "ab", "ac", "abb", "b", "cd", "aaa", "az"}
	batch := func() []bool {
		r := make([]bool, 0, len(pats)*len(ins))
		for _, p := range pats {
			q := nfa.Compile(must(p))
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
	g5 := true
	for g := 0; g < N; g++ {
		if !slices.Equal(res[g], base) {
			g5 = false
		}
	}
	fmt.Println("concurrent 16 goroutines identical:", ok(g5))

	// 6) api：SelfCheck 全过；四类错误可判定且互不相同；被拒后后续调用正常
	g6 := api.SelfCheck() == nil
	rej := []struct {
		p   string
		err error
	}{{"", api.ErrEmptyPattern}, {"a1", api.ErrIllegalChar}, {"(a", api.ErrUnbalancedParen}, {"*", api.ErrMissingOperand}}
	for _, c := range rej {
		if q, e := api.Compile(c.p); q != nil || !errors.Is(e, c.err) {
			g6 = false
		}
	}
	if b, e := api.Match("ab|cd", "ab"); e != nil || !b {
		g6 = false
	}
	fmt.Println("api SelfCheck+4 errors+clean:", ok(g6))
}

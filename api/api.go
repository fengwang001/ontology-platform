// Package api 是「正则 → Thompson NFA → 匹配」的对外入口，依赖 nfa 与 reast。
// 失败一律返回可判定哨兵错误，且任何失败调用都不留下全局状态。
package api

import (
	"errors"
	"maps"
	"slices"

	"ontology/nfa"
	"ontology/reast"
)

// 四类可判定哨兵错误（与 reast 同一实例，errors.Is 可判，且互不相同）。
var (
	ErrEmptyPattern    = reast.ErrEmpty
	ErrIllegalChar     = reast.ErrIllegalChar
	ErrUnbalancedParen = reast.ErrUnbalancedParen
	ErrMissingOperand  = reast.ErrMissingOperand
)

func Compile(pattern string) (*nfa.NFA, error) {
	// 解析并 Thompson 构造；非法 pattern 返回哨兵错误、NFA 为 nil。
	re, err := reast.Parse(pattern)
	if err != nil {
		return nil, err
	}
	return nfa.Compile(re), nil
}
func Match(pattern, s string) (bool, error) {
	// 编译后判定；pattern 非法时返回哨兵错误。
	q, err := Compile(pattern)
	if err != nil {
		return false, err
	}
	return q.Match(s), nil
}
func enum(n int) []string {
	// 循环生成 {a,b,c,ε} 上长度 0..n 的全部串。
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
func mustRE(p string) *reast.RE {
	r, err := reast.Parse(p)
	if err != nil {
		panic(err)
	}
	return r
}

// epsClose 是包外独立实现的 ε 传递闭包，只读导出的 Eps 表。
func epsClose(q *nfa.NFA, seed map[int]struct{}) map[int]struct{} {
	r := maps.Clone(seed)
	st := slices.Collect(maps.Keys(seed))
	for len(st) > 0 {
		x := st[0]
		st = st[1:]
		for _, t := range q.Eps[x] {
			if _, ok := r[t]; !ok {
				r[t] = struct{}{}
				st = append(st, t)
			}
		}
	}
	return r
}

func SelfCheck() error {
	// 核验第二节四条不变量，全过返回 nil，否则返回首个失配错误。
	inputs := enum(5)
	pats := []string{
		"a", "ab", "a|b", "a*", "a+", "a?", "a(b|c)*", "(ab)+", "a?b+",
		"(a|b)*c", "(ab|a)(c|d)*", "x+y*z?", "((a))", "(a|b|c)+",
	}
	for _, p := range pats {
		re := mustRE(p)
		q := nfa.Compile(re)
		for _, s := range inputs {
			if q.Match(s) != reast.ReferenceMatch(re, s) { // 不变量 1：逐串一致
				return errors.New("SelfCheck: NFA/reference mismatch on " + p + " / " + s)
			}
			for i := 0; i <= len(s); i++ { // 不变量 2：每步集合等于自身 ε 闭包
				set := q.After(s[:i])
				if !maps.Equal(set, epsClose(q, set)) {
					return errors.New("SelfCheck: ε-closure not self-consistent on " + p + " / " + s[:i])
				}
			}
		}
	}
	if err := suffixCheck(inputs); err != nil { // 不变量 3
		return err
	}
	return rejectCheck() // 不变量 4
}
func suffixCheck(inputs []string) error {
	// R+≡R·R*、R?≡R|ε、R* 接受空串，与展开式逐串同语言。
	pairs := [][2]*reast.RE{
		{mustRE("a+"), mustRE("aa*")},
		{mustRE("(ab)+"), mustRE("ab(ab)*")},
		{mustRE("(a|b)?"), {Root: &reast.Node{Kind: reast.Alt, L: mustRE("(a|b)").Root, R: &reast.Node{Kind: reast.Eps}}}},
	}
	for _, pr := range pairs {
		q1, q2 := nfa.Compile(pr[0]), nfa.Compile(pr[1])
		for _, s := range inputs {
			if q1.Match(s) != q2.Match(s) {
				return errors.New("SelfCheck: suffix expansion mismatch on " + s)
			}
		}
	}
	if !nfa.Compile(mustRE("a*")).Match("") {
		return errors.New("SelfCheck: a* must accept empty string")
	}
	return nil
}
func rejectCheck() error {
	// 四类非法 pattern 各返回专属哨兵、互不相同，且失败不留全局状态。
	type tc struct {
		p   string
		err error
	}
	cases := []tc{
		{"", ErrEmptyPattern}, {"a1", ErrIllegalChar},
		{"(a", ErrUnbalancedParen}, {"a)", ErrUnbalancedParen},
		{"*", ErrMissingOperand}, {"(|a)*", ErrMissingOperand},
	}
	seen := map[error]bool{}
	for _, c := range cases {
		if q, err := Compile(c.p); q != nil || !errors.Is(err, c.err) {
			return errors.New("SelfCheck: wrong rejection for " + c.p)
		}
		seen[c.err] = true
	}
	if len(seen) != 4 {
		return errors.New("SelfCheck: four rejection errors must be distinct")
	}
	if q, err := Compile("ab|cd"); err != nil || !q.Match("ab") {
		return errors.New("SelfCheck: state not clean after rejections")
	}
	return nil
}

// Package api 对外入口：构造即匹配与自检。
package api

import (
	"errors"
	"fmt"

	"ontology/dfa"
	"ontology/nfa"
)

// BuildAndMatch 构造 DFA 并判定 s；NFA 不合法时整体失败，返回哨兵错误。
func BuildAndMatch(n *nfa.NFA, s string) (bool, error) {
	d, err := dfa.Build(n)
	if err != nil {
		return false, err
	}
	return d.Accepts(s), nil
}

// Example 返回第三节推导用的 NFA。
func Example() *nfa.NFA {
	return &nfa.NFA{
		NumStates: 3,
		Trans: map[int]map[byte][]int{
			0: {'a': {1}},
			1: {nfa.Epsilon: {2}, 'b': {1}},
			2: {'c': {2}},
		},
		Start:    0,
		Accept:   map[int]bool{2: true},
		Alphabet: []byte{'a', 'b', 'c'},
	}
}

// epsCycleNFA 含 ε 环 0⇄1 与 0-a->2，接受 {2}。
func epsCycleNFA() *nfa.NFA {
	return &nfa.NFA{
		NumStates: 3,
		Trans: map[int]map[byte][]int{
			0: {nfa.Epsilon: {1}, 'a': {2}},
			1: {nfa.Epsilon: {0}},
		},
		Start:    0,
		Accept:   map[int]bool{2: true},
		Alphabet: []byte{'a'},
	}
}

// simulate 参照实现：不构造 DFA，直接逐字符 ε闭包+move 模拟。
func simulate(n *nfa.NFA, s string) bool {
	cur := n.EpsilonClosure(map[int]bool{n.Start: true})
	for i := 0; i < len(s); i++ {
		cur = n.EpsilonClosure(n.Move(cur, s[i]))
	}
	for q := range cur {
		if n.Accept[q] {
			return true
		}
	}
	return false
}

// badCase 是一条故障注入用例。
type badCase struct {
	n    *nfa.NFA
	want error
}

// badCases 四类故障注入，哨兵错误互不相同。
func badCases() []badCase {
	base := Example()
	clone := func() *nfa.NFA { c := *base; return &c }
	badStart := clone()
	badStart.Start = 7
	badTarget := clone()
	badTarget.Trans = map[int]map[byte][]int{0: {'a': {9}}}
	noAlpha := clone()
	noAlpha.Alphabet = nil
	noAccept := clone()
	noAccept.Accept = nil
	return []badCase{
		{badStart, nfa.ErrStartOutOfRange},
		{badTarget, nfa.ErrBadTarget},
		{noAlpha, nfa.ErrEmptyAlphabet},
		{noAccept, nfa.ErrNoAccept},
	}
}

// enumerate 生成字母表上长度 ≤ maxLen 的全部字符串。
func enumerate(alphabet []byte, maxLen int) []string {
	out := []string{""}
	for cur := []string{""}; len(cur) > 0 && len(cur[0]) < maxLen; {
		var next []string
		for _, s := range cur {
			for _, c := range alphabet {
				next = append(next, s+string(c))
			}
		}
		out = append(out, next...)
		cur = next
	}
	return out
}

// SelfCheck 对一组内置 NFA 核验四条不变量，全部通过返回 nil。
func SelfCheck() error {
	for _, n := range []*nfa.NFA{Example(), epsCycleNFA()} {
		d, err := dfa.Build(n)
		if err != nil {
			return err
		}
		for _, s := range enumerate(n.Alphabet, 5) { // 不变量1：与参照模拟逐串一致
			if d.Accepts(s) != simulate(n, s) {
				return fmt.Errorf("inv1: mismatch on %q", s)
			}
		}
		for i := 0; i < d.NumStates(); i++ { // 不变量2：每个状态都是完整 ε 闭包
			m := map[int]bool{}
			for _, q := range d.State(i) {
				m[q] = true
			}
			if len(n.EpsilonClosure(m)) != len(m) {
				return fmt.Errorf("inv2: state %d not a full closure", i)
			}
		}
		for i := 0; i < d.NumStates(); i++ { // 不变量3：每 (状态,字符) 至多一个后继
			for _, c := range n.Alphabet {
				a, oka := d.Next(i, c)
				b, okb := d.Next(i, c)
				if oka != okb || a != b {
					return fmt.Errorf("inv3: nondeterministic at state %d", i)
				}
			}
		}
	}
	seen := map[error]bool{}
	for _, bc := range badCases() { // 不变量4：四类故障可判定、互不相同、不留痕
		if _, err := BuildAndMatch(bc.n, "a"); !errors.Is(err, bc.want) || seen[bc.want] {
			return fmt.Errorf("inv4: want %v, got %v", bc.want, err)
		}
		seen[bc.want] = true
	}
	if ok, err := BuildAndMatch(Example(), "a"); err != nil || !ok {
		return fmt.Errorf("inv4: valid call broken after failures")
	}
	return nil
}

// Package api 对外入口：构造、匹配与自检。
package api

import (
	"errors"
	"fmt"

	"ontology/dfa"
	"ontology/nfa"
)

// Section3NFA 返回 NOTES.md 第三节的内置 NFA：0-a->1, 1-ε->2, 1-b->1, 2-c->2。
func Section3NFA() *nfa.NFA {
	return &nfa.NFA{
		N: 3,
		Trans: []map[byte][]int{
			{'a': {1}},
			{nfa.Epsilon: {2}, 'b': {1}},
			{'c': {2}},
		},
		Alphabet: []byte{'a', 'b', 'c'},
		Start:    0,
		Accept:   []int{2},
	}
}

// BuildAndMatch 先校验 NFA，再构造 DFA 并判定 s；
// 校验失败整体返回可判定错误，不产生任何 DFA。
func BuildAndMatch(n *nfa.NFA, s string) (bool, error) {
	if err := n.Validate(); err != nil {
		return false, err
	}
	return dfa.Build(n).Accepts(s), nil
}

// Simulate 参照实现：不构造 DFA，直接对 NFA 逐字符做 ε闭包+move 模拟。
func Simulate(n *nfa.NFA, s string) bool {
	acc := make([]bool, n.N)
	for _, a := range n.Accept {
		if a >= 0 && a < n.N {
			acc[a] = true
		}
	}
	cur := n.EpsilonClosure([]int{n.Start})
	for i := 0; i < len(s); i++ {
		var mv []int
		for _, q := range cur {
			if q < len(n.Trans) {
				mv = append(mv, n.Trans[q][s[i]]...)
			}
		}
		if cur = n.EpsilonClosure(mv); len(cur) == 0 {
			return false
		}
	}
	for _, q := range cur {
		if acc[q] {
			return true
		}
	}
	return false
}

// SelfCheck 对内置 NFA 核验四条不变量，全部通过返回 nil。
func SelfCheck() error {
	n := Section3NFA()
	d := dfa.Build(n)
	// 不变量1：与 NFA 模拟参照逐串一致
	for _, s := range []string{"", "a", "ab", "ac", "abb", "b", "z", "abc", "acc", "abbc", "ba"} {
		if d.Accepts(s) != Simulate(n, s) {
			return fmt.Errorf("invariant1: mismatch on %q", s)
		}
	}
	// 不变量2：每个 DFA 状态都是完整 ε 闭包
	for _, st := range d.States {
		if fmt.Sprint(st) != fmt.Sprint(n.EpsilonClosure(st)) {
			return fmt.Errorf("invariant2: %v is not a full epsilon closure", st)
		}
	}
	// 不变量3：确定性——状态集合按内容唯一，转移表每字符至多一个后继
	seen := map[string]bool{}
	for _, st := range d.States {
		if k := fmt.Sprint(st); seen[k] {
			return fmt.Errorf("invariant3: duplicate DFA state %v", st)
		} else {
			seen[k] = true
		}
	}
	// 不变量4：坏输入整体失败，且不影响后续调用
	bad := *n
	bad.Start = 99
	if _, err := BuildAndMatch(&bad, "a"); !errors.Is(err, nfa.ErrStartOutOfRange) {
		return errors.New("invariant4: invalid NFA not rejected")
	}
	if ok, err := BuildAndMatch(n, "a"); err != nil || !ok {
		return errors.New("invariant4: valid call broken after rejection")
	}
	return nil
}

// Package nfa 表示含 ε 转移的非确定有限自动机。
package nfa

import (
	"errors"
	"sort"
)

// Epsilon 是转移表中表示 ε 转移的特殊键，字母表不得包含它。
const Epsilon byte = 0

// 四类可判定的哨兵错误，互不相同。
var (
	ErrStartOutOfRange      = errors.New("nfa: start state out of range")
	ErrTransitionOutOfRange = errors.New("nfa: transition target out of range")
	ErrEmptyAlphabet        = errors.New("nfa: empty alphabet")
	ErrNoAcceptStates       = errors.New("nfa: empty accept set")
)

// NFA 状态编号为 0..N-1；Trans[s][c] 是 s 在字符 c（或 Epsilon）上的后继集合。
type NFA struct {
	N        int
	Trans    []map[byte][]int
	Alphabet []byte
	Start    int
	Accept   []int
}

// Validate 校验四类可判定错误；任一不满足即整体失败。
func (n *NFA) Validate() error {
	if len(n.Alphabet) == 0 {
		return ErrEmptyAlphabet
	}
	if len(n.Accept) == 0 {
		return ErrNoAcceptStates
	}
	if n.Start < 0 || n.Start >= n.N {
		return ErrStartOutOfRange
	}
	for _, row := range n.Trans {
		for _, dsts := range row {
			for _, d := range dsts {
				if d < 0 || d >= n.N {
					return ErrTransitionOutOfRange
				}
			}
		}
	}
	return nil
}

// EpsilonClosure 返回 S 的 ε 闭包（含 S 自身及所有经 0 或多条 ε 转移
// 可达的状态），升序去重，是确定性的传递闭包。
func (n *NFA) EpsilonClosure(S []int) []int {
	seen := make([]bool, n.N)
	stack := append([]int(nil), S...)
	var out []int
	for len(stack) > 0 {
		s := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if s < 0 || s >= n.N || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
		if s < len(n.Trans) {
			stack = append(stack, n.Trans[s][Epsilon]...)
		}
	}
	sort.Ints(out)
	return out
}

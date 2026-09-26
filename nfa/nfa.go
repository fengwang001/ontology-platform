// Package nfa 表示含 ε 转移的非确定性有限自动机。
package nfa

import "errors"

// Epsilon 是转移表中 ε 转移的特殊键（字母表不得包含该字节）。
const Epsilon byte = 0

// 四类可判定的哨兵错误，互不相同。
var (
	ErrEmptyAlphabet   = errors.New("nfa: alphabet is empty")
	ErrNoAccept        = errors.New("nfa: accept set is empty")
	ErrStartOutOfRange = errors.New("nfa: start state out of range")
	ErrBadTarget       = errors.New("nfa: transition endpoint out of range")
)

// NFA 状态编号为 0..NumStates-1。
type NFA struct {
	NumStates int
	Trans     map[int]map[byte][]int // from -> 字符(或 Epsilon) -> 目标集合
	Start     int
	Accept    map[int]bool
	Alphabet  []byte
}

// Validate 校验四类故障，全部通过返回 nil。
func (n *NFA) Validate() error {
	if len(n.Alphabet) == 0 {
		return ErrEmptyAlphabet
	}
	if len(n.Accept) == 0 {
		return ErrNoAccept
	}
	if n.Start < 0 || n.Start >= n.NumStates {
		return ErrStartOutOfRange
	}
	for from, m := range n.Trans {
		if from < 0 || from >= n.NumStates {
			return ErrBadTarget
		}
		for _, tos := range m {
			for _, to := range tos {
				if to < 0 || to >= n.NumStates {
					return ErrBadTarget
				}
			}
		}
	}
	return nil
}

// EpsilonClosure 返回 S 经 0 或多条 ε 转移可达的状态全集（含 S 自身，传递闭包）。
func (n *NFA) EpsilonClosure(s map[int]bool) map[int]bool {
	out := make(map[int]bool, len(s))
	stack := make([]int, 0, len(s))
	for q := range s {
		out[q] = true
		stack = append(stack, q)
	}
	for len(stack) > 0 {
		q := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, to := range n.Trans[q][Epsilon] {
			if !out[to] {
				out[to] = true
				stack = append(stack, to)
			}
		}
	}
	return out
}

// Move 返回 S 中状态经字符 c 一步直达的状态集合（不做 ε 闭包）。
func (n *NFA) Move(s map[int]bool, c byte) map[int]bool {
	out := map[int]bool{}
	for q := range s {
		for _, to := range n.Trans[q][c] {
			out[to] = true
		}
	}
	return out
}

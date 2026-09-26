// Package reast 定义正则 AST、递归下降解析与朴素回溯参照匹配。
package reast

import (
	"errors"
	"slices"
)

type Kind int

const (
	Lit Kind = iota
	Star
	Cat
	Alt
	Eps
)

type Node struct {
	Kind Kind
	Ch   byte
	L, R *Node
}
type RE struct{ Root *Node }

// 四类可判定哨兵错误，互不相同。
var ErrEmpty, ErrIllegalChar, ErrUnbalancedParen, ErrMissingOperand = errors.New("reast: empty pattern"), errors.New("reast: illegal character"), errors.New("reast: unbalanced parenthesis"), errors.New("reast: suffix/operator missing operand")

// Parse 把 pattern 解析成 RE；非法输入返回哨兵错误，不产生任何结果。
func Parse(s string) (*RE, error) {
	if s == "" {
		return nil, ErrEmpty
	}
	r, i, err := parseAlt(s, 0, false)
	if err != nil || i != len(s) { // i<len 只可能是顶层多余 ')'
		if err == nil {
			err = ErrUnbalancedParen
		}
		return nil, err
	}
	return &RE{r}, nil
}
func cur(s string, i int) byte {
	if i < len(s) {
		return s[i]
	}
	return 0
}
func parseAlt(s string, i int, in bool) (*Node, int, error) {
	// alt := seq ('|' seq)*；in 表示是否在括号内。
	n, i, err := parseSeq(s, i, in)
	for err == nil && cur(s, i) == '|' {
		i++
		var r *Node
		r, i, err = parseSeq(s, i, in)
		if err != nil {
			return nil, i, err
		}
		n = &Node{Kind: Alt, L: n, R: r}
	}
	return n, i, err
}
func parseSeq(s string, i int, in bool) (*Node, int, error) {
	// seq := atom+
	var n *Node
	for c := cur(s, i); c != 0 && c != '|' && c != ')'; c = cur(s, i) {
		a, j, e := parseAtom(s, i)
		if e != nil {
			return nil, j, e
		}
		if n != nil {
			a = &Node{Kind: Cat, L: n, R: a}
		}
		n, i = a, j
	}
	if n == nil {
		if c := cur(s, i); (c == 0 && in) || (c == ')' && !in) {
			return nil, i, ErrUnbalancedParen // 未闭合 '('，或顶层多余 ')'
		}
		return nil, i, ErrMissingOperand // 空分支/缺操作数：()、(|a)、a|、开头的 *
	}
	return n, i, nil
}
func parseAtom(s string, i int) (*Node, int, error) {
	// atom := (literal | '(' alt ')') ('*'|'+'|'?')*
	var n *Node
	switch c := cur(s, i); {
	case c >= 'a' && c <= 'z':
		i++
		n = &Node{Kind: Lit, Ch: c}
	case c == '(':
		i++
		r, j, e := parseAlt(s, i, true)
		if e != nil {
			return nil, j, e
		}
		i = j
		if cur(s, i) != ')' {
			return nil, i, ErrUnbalancedParen
		}
		i++
		n = r
	case c == '*' || c == '+' || c == '?':
		return nil, i, ErrMissingOperand
	default:
		return nil, i, ErrIllegalChar
	}
	for c := cur(s, i); c == '*' || c == '+' || c == '?'; c = cur(s, i) {
		i++
		if c == '*' {
			n = &Node{Kind: Star, L: n}
		} else if c == '+' {
			n = &Node{Kind: Cat, L: n, R: &Node{Kind: Star, L: n}} // R+ = R·R*
		} else {
			n = &Node{Kind: Alt, L: n, R: &Node{Kind: Eps}} // R? = R|ε
		}
	}
	return n, i, nil
}
func ends(n *Node, s string, k int) []int {
	// 朴素递归回溯：尝试所有分支，返回 n 作用于 s[k:] 后所有可能结束位置；星=零遍或吃一遍再星。
	switch n.Kind {
	case Lit:
		if k < len(s) && s[k] == n.Ch {
			return []int{k + 1}
		}
	case Eps:
		return []int{k}
	case Cat:
		var r []int
		for _, x := range ends(n.L, s, k) {
			r = append(r, ends(n.R, s, x)...)
		}
		return r
	case Alt:
		return append(ends(n.L, s, k), ends(n.R, s, k)...)
	case Star:
		r := []int{k} // 零遍
		for _, q := range ends(n.L, s, k) {
			r = append(r, ends(&Node{Kind: Star, L: n.L}, s, q)...)
		}
		return r
	}
	return nil
}

// ReferenceMatch 直接对 AST 做朴素递归回溯（不建 NFA），判定是否整串匹配。
func ReferenceMatch(re *RE, s string) bool { return slices.Contains(ends(re.Root, s, 0), len(s)) }

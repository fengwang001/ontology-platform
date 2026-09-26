// Package query 在 pal 回文树上实现查询：Distinct、Total、Count、Longest。
package query

import (
	"errors"

	"ontology/pal"
)

// ErrNotPalindrome 表示 Count 的入参不是回文。
var ErrNotPalindrome = errors.New("query: not a palindrome")

// Q 是某个已建好回文树上的只读查询器，可并发使用。
type Q struct {
	t *pal.Tree
}

// New 返回 t 上的查询器。
func New(t *pal.Tree) *Q { return &Q{t: t} }

// Distinct 返回不同回文子串个数（节点总数减两个根）。
func (q *Q) Distinct() int { return q.t.NumNodes() - 2 }

// Total 返回全部回文子串总个数（含重复），即各节点出现次数之和。
func (q *Q) Total() int {
	sum := 0
	for v := 2; v < q.t.NumNodes(); v++ {
		sum += q.t.Cnt(v)
	}
	return sum
}

// Count 返回回文 sub 在 s 中的出现次数。
// sub 不是回文时返回 ErrNotPalindrome；是不存在的回文时返回 0。
func (q *Q) Count(sub string) (int, error) {
	if !isPal(sub) {
		return 0, ErrNotPalindrome
	}
	// 回文节点 = 从对应根出发、沿其前一半字符「自中心向外」（逆序）走转移。
	v := 1 // 偶根：偶长度回文
	half := len(sub) / 2
	if len(sub)%2 == 1 {
		v = 0 // 奇根
		half = len(sub)/2 + 1
	}
	for i := half - 1; i >= 0; i-- {
		nxt, ok := q.t.Next(v, sub[i])
		if !ok {
			return 0, nil
		}
		v = nxt
	}
	return q.t.Cnt(v), nil
}

// Longest 返回最长回文子串；并列最长时取构建中最早出现的。
func (q *Q) Longest() string {
	best := 1
	for v := 2; v < q.t.NumNodes(); v++ {
		if q.t.Len(v) > q.t.Len(best) {
			best = v
		}
	}
	return q.t.Sub(best)
}

func isPal(s string) bool {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		if s[i] != s[j] {
			return false
		}
	}
	return true
}
